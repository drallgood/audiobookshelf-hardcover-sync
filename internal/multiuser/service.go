package multiuser

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	stdSync "sync"
	"syscall"
	"time"

	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/audiobookshelf"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/api/hardcover"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/config"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/database"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/logger"
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync"
	statepkg "github.com/drallgood/audiobookshelf-hardcover-sync/internal/sync/state"
)

const maxStateFileComponentBytes = 255

// ErrProfileStateFileNameTooLong indicates that a profile-specific state-file
// path component exceeds the supported filename-component baseline.
var ErrProfileStateFileNameTooLong = errors.New("profile-specific state path component exceeds 255 bytes")

// ErrProfileStateFilePathNotAllowed indicates that an API-provided state-file
// path is absolute or escapes the effective data directory.
var ErrProfileStateFilePathNotAllowed = errors.New("profile-specific state file path must remain under the data directory")

// ErrProfileNotFound indicates that a requested active sync profile is absent.
var ErrProfileNotFound = errors.New("sync profile not found")

// ErrSyncAlreadyActive indicates that a profile already has an active sync.
var ErrSyncAlreadyActive = errors.New("sync already active")

// ErrProfileDeleting indicates that profile lifecycle teardown is in progress.
var ErrProfileDeleting = errors.New("sync profile is being deleted")

// SyncProfileStatus represents the sync status for a profile
type SyncProfileStatus struct {
	ProfileID        string             `json:"profile_id"`
	ProfileName      string             `json:"profile_name"`
	LastAttemptedAt  *time.Time         `json:"last_attempted_at,omitempty"`
	LastSuccessfulAt *time.Time         `json:"last_successful_at,omitempty"`
	Snapshot         *sync.SyncSnapshot `json:"snapshot,omitempty"`
}

// AcceptedSyncRun is the immutable identity returned for a successfully
// accepted start. Its value fields are copied from the durable queued
// reservation before worker launch, so callers never need to infer the
// accepted run from a later status read. The HTTP response may race worker
// execution; response delivery ordering is not part of this contract.
type AcceptedSyncRun struct {
	RunID    string    `json:"run_id"`
	QueuedAt time.Time `json:"queued_at"`
	State    string    `json:"state"`
	DryRun   bool      `json:"dry_run"`
}

type activeSyncRun struct {
	generation  uint64
	runID       string
	startedAt   time.Time
	profileName string
	dryRun      bool
	canceled    bool
}

// profileRunGate serializes lifecycle decisions for one profile without
// serializing unrelated profiles. Repository operations may block while this
// gate is held, but they never hold the service-wide map lock. Workers receive
// FIFO execution tickets while holding the same short-lived lifecycle lock;
// waiting for a ticket never holds that lock, so cancellation can accept a
// replacement while an older worker is still unwinding.
type profileRunGate struct {
	mu                     stdSync.Mutex
	executionCond          *stdSync.Cond
	nextExecutionTicket    uint64
	servingExecutionTicket uint64
	startWaitGroup         stdSync.WaitGroup
	workerWaitGroup        stdSync.WaitGroup
	deleted                bool
}

// MultiUserService manages sync operations for multiple users
type MultiUserService struct {
	repository       *database.Repository
	logger           *logger.Logger
	globalConfig     *config.Config
	profileStatuses  map[string]*SyncProfileStatus
	statusMutex      stdSync.RWMutex
	activeSyncs      map[string]context.CancelFunc
	activeRuns       map[string]activeSyncRun
	syncMutex        stdSync.RWMutex
	syncWaitGroup    stdSync.WaitGroup
	syncServices     map[string]*sync.Service // Maps profile ID to its sync service
	serviceRuns      map[string]uint64
	latestRuns       map[string]activeSyncRun
	profileGates     map[string]*profileRunGate
	servicesMutex    stdSync.RWMutex
	admissionMutex   stdSync.Mutex
	deletingProfiles map[string]struct{}
	// deletedProfiles are lifecycle tombstones: they prevent late callbacks or
	// new admissions from recreating a removed profile's gate after cleanup.
	deletedProfiles       map[string]struct{}
	startWaitGroup        stdSync.WaitGroup
	shutdownMutex         stdSync.Mutex
	cancellationWaitGroup stdSync.WaitGroup
	shuttingDown          bool
}

// NewMultiUserService creates a new multi-user service
func NewMultiUserService(repo *database.Repository, globalConfig *config.Config, log *logger.Logger) *MultiUserService {
	return &MultiUserService{
		repository:       repo,
		logger:           log,
		globalConfig:     globalConfig,
		profileStatuses:  make(map[string]*SyncProfileStatus),
		activeSyncs:      make(map[string]context.CancelFunc),
		activeRuns:       make(map[string]activeSyncRun),
		syncServices:     make(map[string]*sync.Service),
		serviceRuns:      make(map[string]uint64),
		latestRuns:       make(map[string]activeSyncRun),
		profileGates:     make(map[string]*profileRunGate),
		deletingProfiles: make(map[string]struct{}),
		deletedProfiles:  make(map[string]struct{}),
	}
}

// ReconcileInterruptedSyncRuns marks durable queued or otherwise non-terminal
// reports failed before normal multi-user work is admitted after a restart.
func (s *MultiUserService) ReconcileInterruptedSyncRuns() error {
	if s.repository == nil {
		return errors.New("database repository is required")
	}
	return s.repository.ReconcileInterruptedSyncRunReports()
}

// ErrServiceShuttingDown indicates that a new sync was rejected because the
// service is closing its admission gate.
var ErrServiceShuttingDown = errors.New("multi-user service is shutting down")

// beginSyncStart admits one start operation and tracks it until its worker is
// registered. The admission lock is held only for this state transition; all
// repository operations remain outside global lifecycle locks.
func (s *MultiUserService) beginSyncStart(profileID string) (*profileRunGate, error) {
	s.admissionMutex.Lock()
	defer s.admissionMutex.Unlock()
	if s.shuttingDown {
		return nil, ErrServiceShuttingDown
	}
	if _, deleting := s.deletingProfiles[profileID]; deleting {
		return nil, ErrProfileDeleting
	}
	if _, deleted := s.deletedProfiles[profileID]; deleted {
		return nil, ErrProfileNotFound
	}
	gate := s.profileGate(profileID)
	s.startWaitGroup.Add(1)
	gate.startWaitGroup.Add(1)
	return gate, nil
}

func (s *MultiUserService) endSyncStart(gate *profileRunGate) {
	gate.startWaitGroup.Done()
	s.startWaitGroup.Done()
}

// ListProfiles returns all active sync profiles
func (s *MultiUserService) ListProfiles() ([]database.SyncProfile, error) {
	return s.repository.ListProfiles()
}

// ListProfilesForUser returns profiles visible to an authenticated user.
func (s *MultiUserService) ListProfilesForUser(userID string, admin, authEnabled bool) ([]database.SyncProfile, error) {
	return s.repository.ListProfilesForUser(userID, admin, authEnabled)
}

// GetProfileMetadata returns a profile without decrypting its credentials.
func (s *MultiUserService) GetProfileMetadata(profileID string) (*database.SyncProfile, error) {
	return s.repository.GetProfileMetadata(profileID)
}

// GetProfile returns a specific profile with decrypted tokens
func (s *MultiUserService) GetProfile(profileID string) (*database.ProfileWithTokens, error) {
	return s.repository.GetProfile(profileID)
}

// CreateProfile creates a new sync profile
func (s *MultiUserService) CreateProfile(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
	return s.CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig, "")
}

// CreateProfileForUser creates a profile owned by ownerUserID.
func (s *MultiUserService) CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData, ownerUserID string) error {
	if err := s.validateProfileStateFile(profileID, syncConfig.StateFile); err != nil {
		return err
	}
	if err := s.repository.CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig, ownerUserID); err != nil {
		return err
	}
	s.admissionMutex.Lock()
	delete(s.deletedProfiles, profileID)
	s.admissionMutex.Unlock()
	return nil
}

// UpdateProfile updates profile information
func (s *MultiUserService) UpdateProfile(profileID, name string) error {
	return s.repository.UpdateProfile(profileID, name)
}

// UpdateProfileConfig updates profile configuration
func (s *MultiUserService) UpdateProfileConfig(profileID, audiobookshelfURL, audiobookshelfToken, hardcoverToken string, syncConfig database.SyncConfigData) error {
	if syncConfig.StateFile != "" {
		if err := s.validateProfileStateFile(profileID, syncConfig.StateFile); err != nil {
			return err
		}
	}
	return s.repository.UpdateUserConfig(profileID, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig)
}

// DeleteProfile deletes a sync profile
func (s *MultiUserService) DeleteProfile(profileID string) error {
	s.admissionMutex.Lock()
	if _, deleting := s.deletingProfiles[profileID]; deleting {
		s.admissionMutex.Unlock()
		return ErrProfileDeleting
	}
	if _, deleted := s.deletedProfiles[profileID]; deleted {
		s.admissionMutex.Unlock()
		return ErrProfileNotFound
	}
	s.deletingProfiles[profileID] = struct{}{}
	gate := s.profileGate(profileID)
	s.admissionMutex.Unlock()

	gate.mu.Lock()
	cancelErr := s.cancelSyncLocked(context.Background(), profileID, gate)
	if cancelErr != nil && !strings.Contains(cancelErr.Error(), "no active sync") {
		if s.logger != nil {
			s.logger.Warn("Failed to cancel sync during profile deletion", map[string]interface{}{
				"profileID": profileID,
				"error":     cancelErr,
			})
		}
	}
	gate.deleted = true
	deleteErr := s.repository.DeleteProfile(profileID)
	if deleteErr != nil {
		gate.deleted = false
		gate.mu.Unlock()
		s.admissionMutex.Lock()
		delete(s.deletingProfiles, profileID)
		s.admissionMutex.Unlock()
		return deleteErr
	}
	gate.mu.Unlock()

	// Starts admitted before the deletion marker wait at the gate; workers are
	// drained before removing lifecycle maps so late terminal callbacks observe
	// the deleted gate and cannot persist a report for the deleted profile.
	gate.startWaitGroup.Wait()
	gate.workerWaitGroup.Wait()
	s.statusMutex.Lock()
	delete(s.profileStatuses, profileID)
	s.statusMutex.Unlock()
	s.syncMutex.Lock()
	delete(s.activeRuns, profileID)
	delete(s.activeSyncs, profileID)
	delete(s.latestRuns, profileID)
	s.syncMutex.Unlock()
	s.servicesMutex.Lock()
	delete(s.syncServices, profileID)
	delete(s.serviceRuns, profileID)
	s.servicesMutex.Unlock()
	s.syncMutex.Lock()
	delete(s.profileGates, profileID)
	s.syncMutex.Unlock()
	s.admissionMutex.Lock()
	delete(s.deletingProfiles, profileID)
	s.deletedProfiles[profileID] = struct{}{}
	s.admissionMutex.Unlock()
	return nil
}

// GetAllProfileStatuses returns the sync status for all profiles
func (s *MultiUserService) GetAllProfileStatuses() ([]*SyncProfileStatus, error) {
	profiles, err := s.repository.ListProfiles()
	if err != nil {
		return nil, fmt.Errorf("failed to list profiles: %w", err)
	}

	statuses := make([]*SyncProfileStatus, 0, len(profiles))

	for _, profile := range profiles {
		statuses = append(statuses, s.getAggregateProfileStatus(profile))
	}

	return statuses, nil
}

// getAggregateProfileStatus reads the active service's scalar snapshot while
// holding the profile-run lock that keeps service replacement from racing the
// read. Stored status is used for inactive runs so terminal and setup errors
// remain visible. Detailed outcome records are loaded only for an exact run.
func (s *MultiUserService) getAggregateProfileStatus(profile database.SyncProfile) *SyncProfileStatus {
	status := s.getStoredAggregateStatus(profile)
	if status == nil {
		// A freshly constructed service has no in-memory status yet. Restore
		// only scalar fields from the durable terminal report; this is intentionally outside the
		// lifecycle map lock in normal callers, and cacheStatusIfAbsent avoids
		// replacing a run accepted concurrently with this lookup.
		if restored, err := s.restoreAggregateProfileStatus(profile.ID, &profile); err == nil && restored != nil {
			status = s.cacheStatusIfAbsent(profile.ID, restored)
		}
	}
	s.syncMutex.RLock()
	service, generation := s.currentSyncServiceLocked(profile.ID)
	if service == nil {
		s.syncMutex.RUnlock()
		return aggregateProfileStatus(profile, status)
	}

	snapshot := service.GetSnapshotStatus()
	if run, ok := s.activeRuns[profile.ID]; ok && run.generation == generation {
		snapshot.ProfileID = profile.ID
		snapshot.RunID = run.runID
		if snapshot.State == "" || snapshot.State == "idle" {
			snapshot.State = string(sync.RunPhaseQueued)
		}
	}
	s.syncMutex.RUnlock()
	if status == nil {
		status = &SyncProfileStatus{
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
		}
	}
	if status.ProfileID == "" {
		status.ProfileID = profile.ID
	}
	if status.ProfileName == "" {
		status.ProfileName = profile.Name
	}
	status.Snapshot = &snapshot

	return aggregateProfileStatus(profile, status)
}

// getStoredAggregateStatus copies only the scalar fields needed by aggregate
// polling. In particular, terminal snapshots are reduced without copying
// their per-book outcomes.
func (s *MultiUserService) getStoredAggregateStatus(profile database.SyncProfile) *SyncProfileStatus {
	s.statusMutex.RLock()
	defer s.statusMutex.RUnlock()

	stored := s.profileStatuses[profile.ID]
	if stored == nil {
		return nil
	}

	status := &SyncProfileStatus{
		ProfileID:   stored.ProfileID,
		ProfileName: stored.ProfileName,
	}
	if stored.LastAttemptedAt != nil {
		lastAttempted := *stored.LastAttemptedAt
		status.LastAttemptedAt = &lastAttempted
	}
	if stored.LastSuccessfulAt != nil {
		lastSuccessful := *stored.LastSuccessfulAt
		status.LastSuccessfulAt = &lastSuccessful
	}
	if stored.Snapshot != nil {
		status.Snapshot = scalarSnapshot(stored.Snapshot)
	}
	return status
}

// aggregateProfileStatus projects a profile status for the unauthenticated
// aggregate endpoint. Detailed book metadata belongs on the authenticated
// run-details endpoint.
func aggregateProfileStatus(profile database.SyncProfile, status *SyncProfileStatus) *SyncProfileStatus {
	if status == nil {
		return &SyncProfileStatus{
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
		}
	}

	aggregate := &SyncProfileStatus{
		ProfileID:        status.ProfileID,
		ProfileName:      status.ProfileName,
		LastAttemptedAt:  status.LastAttemptedAt,
		LastSuccessfulAt: status.LastSuccessfulAt,
	}
	aggregate.Snapshot = status.Snapshot
	return aggregate
}

// scalarSnapshot keeps aggregate polling cheap and prevents the public
// /api/status response from copying per-book outcome records.
// The full snapshot remains available on the authenticated run-details endpoint.
func scalarSnapshot(snapshot *sync.SyncSnapshot) *sync.SyncSnapshot {
	if snapshot == nil {
		return nil
	}
	return &sync.SyncSnapshot{
		ProfileID:           snapshot.ProfileID,
		RunID:               snapshot.RunID,
		QueuedAt:            snapshot.QueuedAt,
		ProcessingStartedAt: snapshot.ProcessingStartedAt,
		LastActivityAt:      snapshot.LastActivityAt,
		LastProcessedAt:     snapshot.LastProcessedAt,
		FinishedAt:          snapshot.FinishedAt,
		DryRun:              snapshot.DryRun,
		State:               snapshot.State,
		BooksTotal:          snapshot.BooksTotal,
		ProcessedSoFar:      snapshot.ProcessedSoFar,
		UnattemptedCount:    snapshot.UnattemptedCount,
		OutcomeCounts:       snapshot.OutcomeCounts,
	}
}

func (s *MultiUserService) profileGate(profileID string) *profileRunGate {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	if gate := s.profileGates[profileID]; gate != nil {
		return gate
	}
	gate := &profileRunGate{}
	gate.executionCond = stdSync.NewCond(&gate.mu)
	s.profileGates[profileID] = gate
	return gate
}

// enqueueExecutionLocked reserves a FIFO execution position. The caller must
// hold gate.mu, which is already held by StartSyncWithAcceptedRun while it
// performs the acceptance transition.
func (gate *profileRunGate) enqueueExecutionLocked() uint64 {
	ticket := gate.nextExecutionTicket
	gate.nextExecutionTicket++
	return ticket
}

// waitForExecution waits for this worker's FIFO position without retaining the
// lifecycle mutex during worker execution or external I/O.
func (gate *profileRunGate) waitForExecution(ticket uint64) {
	gate.mu.Lock()
	for ticket != gate.servingExecutionTicket {
		gate.executionCond.Wait()
	}
	gate.mu.Unlock()
}

func (gate *profileRunGate) releaseExecution(ticket uint64) {
	gate.mu.Lock()
	if ticket == gate.servingExecutionTicket {
		gate.servingExecutionTicket++
		gate.executionCond.Broadcast()
	}
	gate.mu.Unlock()
}

func (s *MultiUserService) latestRunIsCurrent(profileID, runID string, generation uint64) bool {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	latest, ok := s.latestRuns[profileID]
	if ok {
		return !latest.canceled && latest.runID == runID && latest.generation == generation
	}
	active, ok := s.activeRuns[profileID]
	return ok && !active.canceled && active.runID == runID && active.generation == generation
}

// latestRunWasCanceled reports whether this exact run was explicitly canceled.
// A worker may finish with a generic failure after its context is canceled; the
// cancellation decision remains the truthful terminal outcome and must not be
// overwritten by that late worker report.
func (s *MultiUserService) latestRunWasCanceled(profileID, runID string, generation uint64) bool {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	latest, ok := s.latestRuns[profileID]
	if !ok {
		latest, ok = s.activeRuns[profileID]
	}
	return ok && latest.canceled && latest.runID == runID && latest.generation == generation
}

func terminalReportPhase(phase string) bool {
	return phase == database.SyncRunPhaseCompleted ||
		phase == database.SyncRunPhaseCanceled ||
		phase == database.SyncRunPhaseFailed
}

func copyTime(value *time.Time) *time.Time {
	if value == nil {
		return nil
	}
	copyOf := value.UTC()
	return &copyOf
}

func timeValue(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copyOf := value.UTC()
	return &copyOf
}

// sanitizeReportURL strips credentials and query/fragment data before a
// snapshot is retained. Sync snapshots are already sanitized at their source,
// but keeping this boundary defensive prevents future fields from leaking
// profile credentials into durable reports.
func sanitizeReportURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	parsed.User = nil
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String()
}

func sanitizedSnapshot(snapshot sync.SyncSnapshot) sync.SyncSnapshot {
	copyOf := snapshot
	copyOf.AudiobookshelfURL = sanitizeReportURL(snapshot.AudiobookshelfURL)
	copyOf.BookOutcomes = append([]sync.BookOutcomeRecord(nil), snapshot.BookOutcomes...)
	for i := range copyOf.BookOutcomes {
		copyOf.BookOutcomes[i].CoverURL = sanitizeReportURL(copyOf.BookOutcomes[i].CoverURL)
		copyOf.BookOutcomes[i].HardcoverCoverURL = sanitizeReportURL(copyOf.BookOutcomes[i].HardcoverCoverURL)
	}
	return copyOf
}

func runReportFromSnapshot(profileID string, generation uint64, snapshot sync.SyncSnapshot) (*database.SyncRunReport, error) {
	snapshot = sanitizedSnapshot(snapshot)
	snapshotJSON, err := json.Marshal(snapshot)
	if err != nil {
		return nil, fmt.Errorf("marshal sync snapshot: %w", err)
	}
	phase := database.SyncRunPhaseFailed
	switch snapshot.State {
	case string(sync.RunPhaseCompleted):
		phase = database.SyncRunPhaseCompleted
	case string(sync.RunPhaseCanceled):
		phase = database.SyncRunPhaseCanceled
	case string(sync.RunPhaseFailed):
		phase = database.SyncRunPhaseFailed
	}
	return &database.SyncRunReport{
		ProfileID:           profileID,
		RunID:               snapshot.RunID,
		Generation:          generation,
		Phase:               phase,
		DryRun:              snapshot.DryRun,
		QueuedAt:            timeValue(snapshot.QueuedAt),
		ProcessingStartedAt: timeValue(snapshot.ProcessingStartedAt),
		LastActivityAt:      timeValue(snapshot.LastActivityAt),
		LastProcessedAt:     timeValue(snapshot.LastProcessedAt),
		FinishedAt:          timeValue(snapshot.FinishedAt),
		RunError:            snapshot.RunError,
		SnapshotJSON:        string(snapshotJSON),
	}, nil
}

func (s *MultiUserService) cacheStatusIfAbsent(profileID string, status *SyncProfileStatus) *SyncProfileStatus {
	if status == nil {
		return nil
	}
	s.statusMutex.Lock()
	defer s.statusMutex.Unlock()
	if existing := s.profileStatuses[profileID]; existing != nil {
		return cloneProfileStatus(existing)
	}
	s.profileStatuses[profileID] = cloneProfileStatus(status)
	return cloneProfileStatus(status)
}

// restoreAggregateProfileStatus rebuilds only the scalar status needed by
// aggregate polling. Retained per-book arrays are deliberately excluded from
// SnapshotJSON decoding; exact run details restore the full retained report.
func (s *MultiUserService) restoreAggregateProfileStatus(profileID string, profile *database.SyncProfile) (*SyncProfileStatus, error) {
	if s.repository == nil {
		return nil, nil
	}
	state, err := s.repository.GetSyncState(profileID)
	if err != nil {
		return nil, err
	}
	reports, err := s.repository.ListTerminalSyncRunReports(profileID, 0)
	if err != nil {
		return nil, err
	}

	status := &SyncProfileStatus{ProfileID: profileID}
	if profile != nil {
		status.ProfileName = profile.Name
	}
	if state != nil {
		status.LastAttemptedAt = copyTime(state.LastAttemptedAt)
		status.LastSuccessfulAt = copyTime(state.LastSuccessfulAt)
	}
	if len(reports) == 0 {
		return status, nil
	}

	report := &reports[0]
	snapshot, err := scalarSnapshotFromRetainedReport(profileID, report)
	if err != nil {
		return nil, err
	}
	status.Snapshot = snapshot
	if status.LastAttemptedAt == nil {
		status.LastAttemptedAt = copyTime(report.QueuedAt)
	}
	if report.Phase == database.SyncRunPhaseCompleted && !report.DryRun && status.LastSuccessfulAt == nil {
		status.LastSuccessfulAt = copyTime(report.FinishedAt)
	}
	return status, nil
}

// scalarSnapshotFromRetainedReport decodes only aggregate fields from a
// retained report. Unknown per-book fields in SnapshotJSON are ignored by the
// narrow wire type and are never materialized.
func scalarSnapshotFromRetainedReport(profileID string, report *database.SyncRunReport) (*sync.SyncSnapshot, error) {
	if report == nil {
		return nil, nil
	}
	var scalar struct {
		BooksTotal       int32              `json:"books_total"`
		ProcessedSoFar   int32              `json:"processed_so_far"`
		UnattemptedCount int32              `json:"unattempted_count"`
		OutcomeCounts    sync.OutcomeCounts `json:"outcome_counts"`
	}
	if report.SnapshotJSON != "" && report.SnapshotJSON != "{}" {
		if err := json.Unmarshal([]byte(report.SnapshotJSON), &scalar); err != nil {
			return nil, fmt.Errorf("decode retained sync report %s: %w", report.RunID, err)
		}
	}
	snapshot := &sync.SyncSnapshot{
		ProfileID: profileID, RunID: report.RunID, State: reportPhaseToSyncPhase(report.Phase),
		DryRun: report.DryRun, RunError: report.RunError,
		UnattemptedCount: scalar.UnattemptedCount, BooksTotal: scalar.BooksTotal,
		ProcessedSoFar: scalar.ProcessedSoFar,
		OutcomeCounts:  scalar.OutcomeCounts,
	}
	if report.QueuedAt != nil {
		snapshot.QueuedAt = report.QueuedAt.UTC()
	}
	if report.ProcessingStartedAt != nil {
		snapshot.ProcessingStartedAt = report.ProcessingStartedAt.UTC()
	}
	if report.LastActivityAt != nil {
		snapshot.LastActivityAt = report.LastActivityAt.UTC()
	}
	if report.LastProcessedAt != nil {
		snapshot.LastProcessedAt = report.LastProcessedAt.UTC()
	}
	if report.FinishedAt != nil {
		snapshot.FinishedAt = report.FinishedAt.UTC()
	}
	return snapshot, nil
}

func reportPhaseToSyncPhase(phase string) string {
	switch phase {
	case database.SyncRunPhaseCompleted:
		return string(sync.RunPhaseCompleted)
	case database.SyncRunPhaseCanceled:
		return string(sync.RunPhaseCanceled)
	case database.SyncRunPhaseFailed:
		return string(sync.RunPhaseFailed)
	default:
		return string(sync.RunPhaseFailed)
	}
}

func snapshotFromRetainedReport(profileID string, report *database.SyncRunReport) (*sync.SyncSnapshot, error) {
	if report == nil || !terminalReportPhase(report.Phase) {
		return nil, nil
	}
	var snapshot sync.SyncSnapshot
	if report.SnapshotJSON != "" && report.SnapshotJSON != "{}" {
		if err := json.Unmarshal([]byte(report.SnapshotJSON), &snapshot); err != nil {
			return nil, fmt.Errorf("decode retained sync report %s: %w", report.RunID, err)
		}
	}
	snapshot.ProfileID = profileID
	snapshot.RunID = report.RunID
	snapshot.State = reportPhaseToSyncPhase(report.Phase)
	snapshot.DryRun = report.DryRun
	if report.QueuedAt != nil {
		snapshot.QueuedAt = report.QueuedAt.UTC()
	}
	if report.ProcessingStartedAt != nil {
		snapshot.ProcessingStartedAt = report.ProcessingStartedAt.UTC()
	}
	if report.LastActivityAt != nil {
		snapshot.LastActivityAt = report.LastActivityAt.UTC()
	}
	if report.LastProcessedAt != nil {
		snapshot.LastProcessedAt = report.LastProcessedAt.UTC()
	}
	if report.FinishedAt != nil {
		snapshot.FinishedAt = report.FinishedAt.UTC()
	}
	snapshot.RunError = report.RunError
	return &snapshot, nil
}

// GetSyncRunSnapshot returns the exact active, cached, or retained snapshot for
// runID. It never substitutes a newer run when the requested ID is unknown.
func (s *MultiUserService) GetSyncRunSnapshot(profileID, runID string) (*sync.SyncSnapshot, error) {
	if runID == "" {
		return nil, nil
	}

	s.statusMutex.RLock()
	var storedSnapshot *sync.SyncSnapshot
	if status := s.profileStatuses[profileID]; status != nil && status.Snapshot != nil && status.Snapshot.RunID == runID {
		storedSnapshot = cloneSyncSnapshot(*status.Snapshot)
	}
	s.statusMutex.RUnlock()

	s.syncMutex.RLock()
	service, generation := s.currentSyncServiceLocked(profileID)
	if service != nil {
		snapshot := service.GetSnapshot()
		if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
			snapshot.ProfileID = profileID
			snapshot.RunID = run.runID
			if snapshot.State == "" || snapshot.State == "idle" {
				snapshot.State = string(sync.RunPhaseQueued)
			}
		}
		s.syncMutex.RUnlock()
		if snapshot.RunID == runID {
			return &snapshot, nil
		}
	} else {
		s.syncMutex.RUnlock()
	}
	if s.repository == nil {
		return storedSnapshot, nil
	}
	report, err := s.repository.GetSyncRunReport(profileID, runID)
	if err != nil {
		return nil, err
	}
	if report != nil && terminalReportPhase(report.Phase) {
		return snapshotFromRetainedReport(profileID, report)
	}
	return storedSnapshot, nil
}

func (s *MultiUserService) currentSyncServiceLocked(profileID string) (*sync.Service, uint64) {
	s.servicesMutex.RLock()
	defer s.servicesMutex.RUnlock()
	service, exists := s.syncServices[profileID]
	generation := s.serviceRuns[profileID]
	activeRun, active := s.activeRuns[profileID]
	if !exists || service == nil {
		return nil, 0
	}
	if generation == 0 {
		return service, 0
	}
	if !active || activeRun.generation != generation {
		return nil, 0
	}
	return service, generation
}

// StartSyncWithAcceptedRun starts a sync and returns the durable queued run
// identity accepted for it. The returned record is constructed from the same
// durable reservation that installed the queued report before worker launch;
// response delivery may race worker execution.
func (s *MultiUserService) StartSyncWithAcceptedRun(profileID string) (AcceptedSyncRun, error) {
	gate, err := s.beginSyncStart(profileID)
	if err != nil {
		return AcceptedSyncRun{}, fmt.Errorf("cannot start sync for profile %s: %w", profileID, err)
	}
	defer s.endSyncStart(gate)

	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.deleted {
		return AcceptedSyncRun{}, fmt.Errorf("cannot start sync for profile %s: %w", profileID, ErrProfileDeleting)
	}

	s.syncMutex.RLock()
	_, alreadyActive := s.activeSyncs[profileID]
	s.syncMutex.RUnlock()
	if alreadyActive {
		return AcceptedSyncRun{}, fmt.Errorf("%w for profile %s", ErrSyncAlreadyActive, profileID)
	}

	// Get profile config
	profileConfig, err := s.GetProfile(profileID)
	if err != nil {
		return AcceptedSyncRun{}, fmt.Errorf("failed to get profile config: %w", err)
	}
	if profileConfig == nil {
		return AcceptedSyncRun{}, fmt.Errorf("failed to get profile config: %w: %s", ErrProfileNotFound, profileID)
	}
	if err := s.validatePersistedProfileStateFile(profileID, profileConfig.SyncConfig.StateFile); err != nil {
		return AcceptedSyncRun{}, fmt.Errorf("invalid persisted state file for profile %s: %w", profileID, err)
	}

	// Prepare the full queued report before accepting the start. The repository
	// assigns its authoritative generation and persists attempt metadata plus
	// this sanitized report in one transaction.
	queuedAt := time.Now().UTC()
	runID := fmt.Sprintf("%s-%d", profileID, queuedAt.UnixNano())
	queuedSnapshot := newRunSnapshot(profileID, activeSyncRun{
		runID:       runID,
		startedAt:   queuedAt,
		profileName: profileConfig.Profile.Name,
		dryRun:      profileConfig.SyncConfig.DryRun,
	}, string(sync.RunPhaseQueued))
	queuedSnapshotJSON, err := marshalSanitizedSnapshot(queuedSnapshot)
	if err != nil {
		return AcceptedSyncRun{}, fmt.Errorf("failed to prepare queued sync report for profile %s: %w", profileID, err)
	}
	queuedReport, err := s.repository.AcceptSyncRun(&database.SyncRunReport{
		ProfileID: profileID, RunID: runID, Phase: database.SyncRunPhaseQueued,
		DryRun: profileConfig.SyncConfig.DryRun, QueuedAt: timeValue(queuedAt),
		SnapshotJSON: queuedSnapshotJSON,
	})
	if err != nil {
		return AcceptedSyncRun{}, fmt.Errorf("failed to accept sync run for profile %s: %w", profileID, err)
	}
	if queuedReport == nil || queuedReport.QueuedAt == nil {
		return AcceptedSyncRun{}, fmt.Errorf("failed to accept sync run for profile %s: incomplete acceptance", profileID)
	}
	accepted := AcceptedSyncRun{
		RunID:    queuedReport.RunID,
		QueuedAt: queuedReport.QueuedAt.UTC(),
		State:    string(sync.RunPhaseQueued),
		DryRun:   queuedReport.DryRun,
	}

	// Create cancellable context and install lifecycle state only after the
	// durable reservation and queued report are complete. Every map mutation is
	// brief; repository calls above never hold syncMutex.
	ctx, cancel := context.WithCancel(context.Background())
	run := activeSyncRun{
		generation:  queuedReport.Generation,
		startedAt:   accepted.QueuedAt,
		profileName: profileConfig.Profile.Name,
		dryRun:      accepted.DryRun,
	}
	run.runID = accepted.RunID
	s.syncMutex.Lock()
	if _, exists := s.activeSyncs[profileID]; exists {
		s.syncMutex.Unlock()
		cancel()
		return AcceptedSyncRun{}, fmt.Errorf("%w for profile %s", ErrSyncAlreadyActive, profileID)
	}
	s.activeSyncs[profileID] = cancel
	s.activeRuns[profileID] = run
	s.latestRuns[profileID] = run
	s.syncMutex.Unlock()

	// Update initial status
	lastSuccessful := s.priorSuccessfulTime(profileID, profileConfig.Profile.SyncState)
	initialStatus := &SyncProfileStatus{
		ProfileID:        profileID,
		ProfileName:      profileConfig.Profile.Name,
		LastAttemptedAt:  timeValue(accepted.QueuedAt),
		LastSuccessfulAt: lastSuccessful,
	}
	applySnapshotToStatus(initialStatus, queuedSnapshot)
	s.updateProfileStatus(profileID, initialStatus)

	// Reserve the worker's profile-local execution position before launching it.
	// A replacement accepted after cancellation therefore cannot overtake this
	// worker even if this goroutine has not been scheduled yet.
	executionTicket := gate.enqueueExecutionLocked()

	// Start the sync in background.
	gate.workerWaitGroup.Add(1)
	s.syncWaitGroup.Add(1)
	go func() {
		defer gate.workerWaitGroup.Done()
		defer s.syncWaitGroup.Done()
		s.runSyncWorker(profileID, run.runID, run.generation, gate, executionTicket, func() {
			s.performSync(ctx, profileID, profileConfig, run.generation)
		})
	}()
	return accepted, nil
}

// Shutdown closes admission to new starts, cancels every active profile run,
// and waits for accepted workers to exit until ctx is done. It is safe to call
// more than once; a later call can continue draining after an earlier timeout.
// Repository operations performed while canceling runs are profile-scoped and
// never hold the service-wide lifecycle lock.
func (s *MultiUserService) Shutdown(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	s.shutdownMutex.Lock()
	defer s.shutdownMutex.Unlock()

	s.admissionMutex.Lock()
	s.shuttingDown = true
	s.admissionMutex.Unlock()

	if err := waitForSyncGroup(ctx, &s.startWaitGroup); err != nil {
		return fmt.Errorf("wait for sync starts to finish: %w", err)
	}
	if err := waitForSyncGroup(ctx, &s.cancellationWaitGroup); err != nil {
		return fmt.Errorf("wait for prior sync cancellations: %w", err)
	}

	if err := s.cancelActiveSyncs(ctx); err != nil {
		return fmt.Errorf("cancel active syncs: %w", err)
	}

	if err := waitForSyncGroup(ctx, &s.syncWaitGroup); err != nil {
		return fmt.Errorf("wait for sync workers to finish: %w", err)
	}
	return nil
}

func (s *MultiUserService) cancelActiveSyncs(ctx context.Context) error {
	profileIDs := s.activeProfileIDs()
	s.cancellationWaitGroup.Add(len(profileIDs))
	for _, profileID := range profileIDs {
		go func() {
			defer s.cancellationWaitGroup.Done()
			if err := s.cancelSync(ctx, profileID); err != nil && s.logger != nil {
				s.logger.Warn("Failed to cancel sync during service shutdown", map[string]interface{}{
					"profile_id": profileID,
					"error":      err,
				})
			}
		}()
	}
	return waitForSyncGroup(ctx, &s.cancellationWaitGroup)
}

func waitForSyncGroup(ctx context.Context, group *stdSync.WaitGroup) error {
	done := make(chan struct{})
	go func() {
		group.Wait()
		close(done)
	}()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *MultiUserService) activeProfileIDs() []string {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	profileIDs := make([]string, 0, len(s.activeSyncs))
	for profileID := range s.activeSyncs {
		profileIDs = append(profileIDs, profileID)
	}
	return profileIDs
}

// CancelSync cancels a running sync operation for a profile.
func (s *MultiUserService) CancelSync(profileID string) error {
	return s.cancelSync(context.Background(), profileID)
}

func (s *MultiUserService) cancelSync(ctx context.Context, profileID string) error {
	s.admissionMutex.Lock()
	if _, deleting := s.deletingProfiles[profileID]; deleting {
		s.admissionMutex.Unlock()
		return ErrProfileDeleting
	}
	if _, deleted := s.deletedProfiles[profileID]; deleted {
		s.admissionMutex.Unlock()
		return ErrProfileNotFound
	}
	gate := s.profileGate(profileID)
	s.admissionMutex.Unlock()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return s.cancelSyncLocked(ctx, profileID, gate)
}

func (s *MultiUserService) cancelSyncLocked(ctx context.Context, profileID string, gate *profileRunGate) error {
	s.syncMutex.RLock()
	cancel, exists := s.activeSyncs[profileID]
	run, runExists := s.activeRuns[profileID]
	service, serviceGeneration := s.currentSyncServiceLocked(profileID)
	s.syncMutex.RUnlock()
	if !exists {
		return fmt.Errorf("no active sync for profile %s", profileID)
	}
	if !runExists {
		return fmt.Errorf("active sync identity missing for profile %s", profileID)
	}

	// Mark the latest run canceled before signaling the worker. This closes the
	// publication race: a worker that observes cancellation late may retain its
	// report, but cannot replace the canceled/current status.
	s.syncMutex.Lock()
	if current, ok := s.activeRuns[profileID]; !ok || current.runID != run.runID || current.generation != run.generation {
		s.syncMutex.Unlock()
		return fmt.Errorf("no active sync for profile %s", profileID)
	}
	run.canceled = true
	s.activeRuns[profileID] = run
	if latest, ok := s.latestRuns[profileID]; ok && latest.runID == run.runID && latest.generation == run.generation {
		latest.canceled = true
		s.latestRuns[profileID] = latest
	}
	delete(s.activeSyncs, profileID)
	s.syncMutex.Unlock()
	cancel()

	// Remove active markers so a replacement can be accepted immediately. The
	// old worker's final publication is generation-checked and cannot win.
	s.syncMutex.Lock()
	if current, ok := s.activeRuns[profileID]; ok && current.runID == run.runID && current.generation == run.generation {
		delete(s.activeRuns, profileID)
	}
	if service != nil && serviceGeneration == run.generation {
		s.servicesMutex.Lock()
		if s.serviceRuns[profileID] == run.generation && s.syncServices[profileID] == service {
			delete(s.syncServices, profileID)
			delete(s.serviceRuns, profileID)
		}
		s.servicesMutex.Unlock()
	}
	s.syncMutex.Unlock()

	snapshot := s.snapshotForCanceledRun(profileID, run, service)
	finalStatus := s.statusForTerminalRun(profileID, run, snapshot)
	if err := s.persistTerminalSnapshotContext(ctx, profileID, run.generation, snapshot); err != nil {
		snapshot.State = string(sync.RunPhaseFailed)
		snapshot.RunError = fmt.Sprintf("failed to persist canceled sync report: %v", err)
		applySnapshotToStatus(finalStatus, snapshot)
		s.publishStatusIfLatest(profileID, run, finalStatus)
		return fmt.Errorf("failed to persist canceled sync report: %w", err)
	}
	s.publishStatusIfLatest(profileID, run, finalStatus)
	return nil
}

// runSyncWorker owns one profile's execution ticket for the complete worker
// lifetime. A canceled or replaced run is rejected after acquiring its turn,
// before migration, client setup, cache loading, or state-file work.
func (s *MultiUserService) runSyncWorker(profileID, runID string, generation uint64, gate *profileRunGate, executionTicket uint64, work func()) {
	gate.waitForExecution(executionTicket)
	defer gate.releaseExecution(executionTicket)
	if !s.latestRunIsCurrent(profileID, runID, generation) {
		return
	}
	work()
}

// performSync performs the actual sync operation for a profile
func (s *MultiUserService) performSync(ctx context.Context, profileID string, profileConfig *database.ProfileWithTokens, generation uint64) {
	// Ensure only this run's active marker is cleared when its goroutine ends.
	defer s.finishActiveRun(profileID, generation)
	// Create profile-specific config
	config := s.createProfileSpecificConfig(profileConfig)
	if err := s.migrateLegacyProfileStatePath(profileID, profileConfig.SyncConfig.StateFile); err != nil {
		status := &SyncProfileStatus{
			ProfileID:   profileID,
			ProfileName: profileConfig.Profile.Name,
		}
		if run, ok := s.activeRun(profileID, generation); ok {
			snapshot := newRunSnapshot(profileID, run, string(sync.RunPhaseFailed))
			snapshot.RunError = fmt.Sprintf("Failed to migrate legacy sync state: %v", err)
			applySnapshotToStatus(status, snapshot)
			s.publishFinalStatus(profileID, generation, status)
		}
		return
	}

	// Create clients
	absClient := audiobookshelf.NewClient(profileConfig.AudiobookshelfURL, profileConfig.AudiobookshelfToken)

	// Build Hardcover client config using global settings (rate limits/base URL)
	hcCfg := hardcover.DefaultClientConfig()
	if s.globalConfig != nil {
		if s.globalConfig.Hardcover.BaseURL != "" {
			hcCfg.BaseURL = s.globalConfig.Hardcover.BaseURL
		}
		if s.globalConfig.RateLimit.Rate > 0 {
			hcCfg.RateLimit = s.globalConfig.RateLimit.Rate
		}
		if s.globalConfig.RateLimit.MaxConcurrent > 0 {
			hcCfg.MaxConcurrent = s.globalConfig.RateLimit.MaxConcurrent
		}
	}

	s.logger.Debug("Initializing Hardcover client (multi-user)", map[string]interface{}{
		"profile_id":     profileID,
		"base_url":       hcCfg.BaseURL,
		"rate_limit":     hcCfg.RateLimit.String(),
		"max_concurrent": hcCfg.MaxConcurrent,
	})

	hcClient := hardcover.NewClientWithConfig(hcCfg, profileConfig.HardcoverToken, s.logger)

	// Create sync service bound to the accepted run identity. This preserves the
	// queued run ID/timestamp through service initialization and Sync startup.
	run, runCurrent := s.activeRun(profileID, generation)
	if !runCurrent {
		return
	}
	syncService, err := sync.NewServiceWithRunIdentity(absClient, hcClient, config, run.runID, run.startedAt)
	if err != nil {
		status := &SyncProfileStatus{
			ProfileID:   profileID,
			ProfileName: profileConfig.Profile.Name,
		}
		if run, ok := s.activeRun(profileID, generation); ok {
			snapshot := newRunSnapshot(profileID, run, string(sync.RunPhaseFailed))
			snapshot.RunError = fmt.Sprintf("Failed to create sync service: %v", err)
			applySnapshotToStatus(status, snapshot)
			s.publishFinalStatus(profileID, generation, status)
		}
		return
	}

	// Store the sync service for status access
	if !s.registerSyncService(profileID, generation, syncService) {
		return
	}
	defer s.removeSyncService(profileID, generation, syncService)

	// Run the sync
	err = syncService.Sync(ctx)

	snapshot := s.normalizeRunSnapshot(profileID, generation, syncService.GetSnapshot())

	// Prepare final status
	status := s.statusForTerminalRun(profileID, run, snapshot)
	applySnapshotToStatus(status, snapshot)
	if status.Snapshot != nil && status.Snapshot.ProfileID == "" {
		status.Snapshot.ProfileID = profileID
	}

	if err != nil {
		if status.Snapshot != nil && status.Snapshot.RunError == "" {
			status.Snapshot.RunError = err.Error()
		}
		s.logger.Error("Sync failed", map[string]interface{}{
			"profileID": profileID,
			"error":     err,
		})
	} else {
		s.logger.Debug("Stored full sync summary in profile status", map[string]interface{}{
			"profileID":       profileID,
			"books_processed": snapshot.ProcessedSoFar,
			"synced_count":    snapshot.OutcomeCounts.Synced,
			"needs_review":    snapshot.OutcomeCounts.NeedsReview,
			"not_found":       snapshot.OutcomeCounts.NotFound,
		})
	}

	// Persist the terminal report without holding the service-wide lifecycle
	// lock, then publish in memory only if run ID and generation are current.
	s.publishFinalStatus(profileID, generation, status)
}

func newRunSnapshot(profileID string, run activeSyncRun, state string) sync.SyncSnapshot {
	snapshot := sync.SyncSnapshot{
		ProfileID:      profileID,
		RunID:          run.runID,
		QueuedAt:       run.startedAt,
		LastActivityAt: run.startedAt,
		State:          state,
		DryRun:         run.dryRun,
		BookOutcomes:   make([]sync.BookOutcomeRecord, 0),
	}
	if state == string(sync.RunPhaseCompleted) || state == string(sync.RunPhaseCanceled) || state == string(sync.RunPhaseFailed) {
		snapshot.FinishedAt = time.Now().UTC()
	}
	return snapshot
}

func marshalSanitizedSnapshot(snapshot sync.SyncSnapshot) (string, error) {
	data, err := json.Marshal(sanitizedSnapshot(snapshot))
	if err != nil {
		return "", fmt.Errorf("marshal sync snapshot: %w", err)
	}
	return string(data), nil
}

func (s *MultiUserService) priorSuccessfulTime(profileID string, profileState *database.ProfileSyncState) *time.Time {
	s.statusMutex.RLock()
	stored := s.profileStatuses[profileID]
	var lastSuccessful *time.Time
	if stored != nil {
		lastSuccessful = copyTime(stored.LastSuccessfulAt)
	}
	s.statusMutex.RUnlock()
	if profileState != nil {
		if lastSuccessful == nil {
			lastSuccessful = copyTime(profileState.LastSuccessfulAt)
		}
	}
	return lastSuccessful
}

func (s *MultiUserService) snapshotForCanceledRun(profileID string, run activeSyncRun, service *sync.Service) sync.SyncSnapshot {
	var snapshot sync.SyncSnapshot
	if service != nil {
		snapshot = service.GetSnapshot()
	} else {
		s.statusMutex.RLock()
		if status := s.profileStatuses[profileID]; status != nil && status.Snapshot != nil {
			snapshot = *cloneSyncSnapshot(*status.Snapshot)
		}
		s.statusMutex.RUnlock()
	}
	snapshot.ProfileID = profileID
	snapshot.RunID = run.runID
	if snapshot.QueuedAt.IsZero() {
		snapshot.QueuedAt = run.startedAt
	}
	if snapshot.LastActivityAt.IsZero() {
		snapshot.LastActivityAt = run.startedAt
	}
	snapshot.State = string(sync.RunPhaseCanceled)
	snapshot.RunError = context.Canceled.Error()
	snapshot.FinishedAt = time.Now().UTC()
	snapshot.DryRun = run.dryRun
	return snapshot
}

func (s *MultiUserService) statusForTerminalRun(profileID string, run activeSyncRun, snapshot sync.SyncSnapshot) *SyncProfileStatus {
	lastSuccessful := s.priorSuccessfulTime(profileID, nil)
	status := &SyncProfileStatus{
		ProfileID:        profileID,
		ProfileName:      run.profileName,
		LastAttemptedAt:  timeValue(run.startedAt),
		LastSuccessfulAt: lastSuccessful,
	}
	applySnapshotToStatus(status, snapshot)
	return status
}

func (s *MultiUserService) persistTerminalSnapshot(profileID string, generation uint64, snapshot sync.SyncSnapshot) error {
	return s.persistTerminalSnapshotContext(context.Background(), profileID, generation, snapshot)
}

func (s *MultiUserService) persistTerminalSnapshotContext(ctx context.Context, profileID string, generation uint64, snapshot sync.SyncSnapshot) error {
	if s.repository == nil {
		return nil
	}
	report, err := runReportFromSnapshot(profileID, generation, snapshot)
	if err != nil {
		return err
	}
	return s.repository.UpsertSyncRunReportContext(ctx, report)
}

func (s *MultiUserService) publishStatusIfLatest(profileID string, run activeSyncRun, status *SyncProfileStatus) bool {
	if status == nil {
		return false
	}
	s.syncMutex.RLock()
	latest, latestExists := s.latestRuns[profileID]
	if !latestExists {
		latest, latestExists = s.activeRuns[profileID]
	}
	s.syncMutex.RUnlock()
	if !latestExists || latest.runID != run.runID || latest.generation != run.generation {
		return false
	}
	if latest.canceled && status.Snapshot != nil && status.Snapshot.State != string(sync.RunPhaseCanceled) && status.Snapshot.State != string(sync.RunPhaseFailed) {
		return false
	}
	if status.Snapshot != nil && status.Snapshot.State == string(sync.RunPhaseCompleted) && !status.Snapshot.DryRun {
		finishedAt := status.Snapshot.FinishedAt
		if !finishedAt.IsZero() {
			status.LastSuccessfulAt = copyTime(&finishedAt)
		}
	}
	s.updateProfileStatus(profileID, status)
	return true
}

func (s *MultiUserService) activeRun(profileID string, generation uint64) (activeSyncRun, bool) {
	if generation == 0 {
		return activeSyncRun{}, false
	}
	s.syncMutex.RLock()
	run, ok := s.activeRuns[profileID]
	s.syncMutex.RUnlock()
	return run, ok && run.generation == generation
}

func (s *MultiUserService) normalizeRunSnapshot(profileID string, generation uint64, snapshot sync.SyncSnapshot) sync.SyncSnapshot {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
		snapshot.ProfileID = profileID
		snapshot.RunID = run.runID
		if snapshot.State == "" || snapshot.State == "idle" {
			snapshot.State = string(sync.RunPhaseQueued)
		}
	}
	if snapshot.ProfileID == "" {
		snapshot.ProfileID = profileID
	}
	return snapshot
}

func (s *MultiUserService) registerSyncService(profileID string, generation uint64, service *sync.Service) bool {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	run, ok := s.activeRuns[profileID]
	if !ok || run.generation != generation {
		return false
	}
	s.servicesMutex.Lock()
	s.syncServices[profileID] = service
	s.serviceRuns[profileID] = generation
	s.servicesMutex.Unlock()
	return true
}

func (s *MultiUserService) removeSyncService(profileID string, generation uint64, service *sync.Service) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	s.servicesMutex.Lock()
	if s.syncServices[profileID] == service && s.serviceRuns[profileID] == generation {
		delete(s.syncServices, profileID)
		delete(s.serviceRuns, profileID)
	}
	s.servicesMutex.Unlock()
}

func (s *MultiUserService) finishActiveRun(profileID string, generation uint64) {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
		delete(s.activeRuns, profileID)
		delete(s.activeSyncs, profileID)
	}
}

func (s *MultiUserService) publishFinalStatus(profileID string, generation uint64, status *SyncProfileStatus) bool {
	if status == nil {
		return false
	}
	if status.Snapshot == nil {
		return false
	}
	runID := status.Snapshot.RunID
	if runID == "" {
		s.syncMutex.RLock()
		run, ok := s.activeRuns[profileID]
		s.syncMutex.RUnlock()
		if !ok || run.generation != generation {
			return false
		}
		runID = run.runID
		status.Snapshot.RunID = runID
	}
	// Serialize same-profile terminal persistence with start/cancel decisions.
	// This does not serialize unrelated profiles. Every terminal report must
	// still belong to the current, uncanceled accepted run before persistence;
	// otherwise a stale failed/canceled worker could overwrite its durable row
	// after a replacement has been accepted.
	s.admissionMutex.Lock()
	if _, deleting := s.deletingProfiles[profileID]; deleting {
		s.admissionMutex.Unlock()
		return false
	}
	if _, deleted := s.deletedProfiles[profileID]; deleted {
		s.admissionMutex.Unlock()
		return false
	}
	gate := s.profileGate(profileID)
	s.admissionMutex.Unlock()
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if gate.deleted || !s.latestRunIsCurrent(profileID, runID, generation) {
		return false
	}
	// Durable report persistence is deliberately outside syncMutex. A blocked
	// database operation for one profile must not stall status or start/cancel
	// operations for another profile.
	if err := s.persistTerminalSnapshot(profileID, generation, *status.Snapshot); err != nil {
		status = cloneProfileStatus(status)
		reportError := fmt.Sprintf("failed to persist sync report: %v", err)
		status.Snapshot.State = string(sync.RunPhaseFailed)
		status.Snapshot.RunError = reportError
		s.publishStatusIfLatest(profileID, activeSyncRun{generation: generation, runID: runID}, status)
		if s.logger != nil {
			s.logger.Error("Failed to persist terminal sync report", map[string]interface{}{
				"profileID":  profileID,
				"generation": generation,
				"error":      err,
			})
		}
		return false
	}

	return s.publishStatusIfLatest(profileID, activeSyncRun{generation: generation, runID: runID}, status)
}

// createProfileSpecificConfig creates a config.Config instance for a specific profile
func (s *MultiUserService) createProfileSpecificConfig(profileConfig *database.ProfileWithTokens) *config.Config {
	// Create a copy of the global config
	config := *s.globalConfig
	// Scope mismatch exports to this profile so one profile's cleanup cannot
	// remove another profile's reports when they share the global base path.
	if config.Paths.MismatchOutputDir != "" {
		config.Paths.MismatchOutputDir = filepath.Join(
			config.Paths.MismatchOutputDir,
			encodeProfileID(profileConfig.Profile.ID),
		)
	}

	// Override with profile-specific settings
	config.Audiobookshelf.URL = profileConfig.AudiobookshelfURL
	config.Audiobookshelf.Token = profileConfig.AudiobookshelfToken
	config.Hardcover.Token = profileConfig.HardcoverToken

	// Apply sync config from profile
	syncConfig := profileConfig.SyncConfig

	// Always apply the sync config from the profile
	// The config in the profile should already have the correct values (either defaults or explicitly set)

	// Parse sync interval if provided
	if syncConfig.SyncInterval != "" {
		duration, err := time.ParseDuration(syncConfig.SyncInterval)
		if err != nil {
			s.logger.Warn("Invalid sync interval, using default", map[string]interface{}{
				"profileID": profileConfig.Profile.ID,
				"interval":  syncConfig.SyncInterval,
				"error":     err,
			})
			duration = 1 * time.Hour // Default to 1 hour if invalid
		}
		config.Sync.SyncInterval = duration
	}

	// Apply all sync config values from the profile
	config.Sync.Incremental = syncConfig.Incremental
	// Make state file path profile-specific to avoid conflicts. Keep this
	// derivation centralized because profile creation/update validation must
	// inspect the exact path used by runtime setup.
	config.Sync.StateFile = s.profileSpecificStatePath(profileConfig.Profile.ID, syncConfig.StateFile)
	config.Sync.MinChangeThreshold = syncConfig.MinChangeThreshold
	config.Sync.Libraries.Include = syncConfig.Libraries.Include
	config.Sync.Libraries.Exclude = syncConfig.Libraries.Exclude
	config.Sync.MinimumProgress = syncConfig.MinimumProgress
	config.Sync.SyncWantToRead = syncConfig.SyncWantToRead
	config.Sync.ProcessUnreadBooks = syncConfig.ProcessUnreadBooks
	config.Sync.SyncOwned = syncConfig.SyncOwned
	config.Sync.IncludeEbooks = syncConfig.IncludeEbooks
	config.Sync.DryRun = syncConfig.DryRun
	config.Sync.TestBookFilter = syncConfig.TestBookFilter
	config.Sync.TestBookLimit = syncConfig.TestBookLimit
	config.Audiobookshelf.AudnexusRegion = syncConfig.AudnexusRegion

	// Debug logging to verify the config is being applied correctly
	s.logger.Debug("Applied sync config for profile", map[string]interface{}{
		"profileID":            profileConfig.Profile.ID,
		"process_unread_books": config.Sync.ProcessUnreadBooks,
		"incremental":          config.Sync.Incremental,
		"sync_want_to_read":    config.Sync.SyncWantToRead,
	})

	return &config
}

func (s *MultiUserService) profileSpecificStatePath(profileID, configuredPath string) string {
	return fmt.Sprintf("%s.%s", strings.TrimSuffix(s.profileStateBasePath(configuredPath), ".json"), encodeProfileID(profileID))
}

func (s *MultiUserService) legacyProfileStatePath(profileID, configuredPath string) string {
	return fmt.Sprintf("%s.%s", strings.TrimSuffix(s.profileStateBasePath(configuredPath), ".json"), profileID)
}

func (s *MultiUserService) profileStateBasePath(configuredPath string) string {
	statePath := configuredPath
	if statePath == "" {
		statePath = filepath.Join(s.effectiveDataDir(), "sync_state.json")
	} else if !filepath.IsAbs(statePath) {
		statePath = filepath.Join(s.effectiveDataDir(), statePath)
	}
	return statePath
}

// migrateLegacyProfileStatePath preserves state written before profile IDs were
// encoded into a single filename component. It only reads a regular, non-symlink
// file whose lexical and resolved parent paths remain under the canonical state
// file directory. The canonical copy is written atomically by State.Save before
// the old file is renamed to a recoverable .migrated backup.
func (s *MultiUserService) migrateLegacyProfileStatePath(profileID, configuredPath string) error {
	canonicalPath, err := filepath.Abs(s.profileSpecificStatePath(profileID, configuredPath))
	if err != nil {
		return fmt.Errorf("resolve canonical state path: %w", err)
	}
	if _, err := os.Lstat(canonicalPath); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect canonical state path: %w", err)
	}
	if !safeLegacyProfileIDPathSegments(profileID) {
		return nil
	}

	legacyPath, err := filepath.Abs(s.legacyProfileStatePath(profileID, configuredPath))
	if err != nil {
		return fmt.Errorf("resolve legacy state path: %w", err)
	}
	if legacyPath == canonicalPath {
		return nil
	}

	stateDir := filepath.Dir(canonicalPath)
	if !pathWithinDirectory(stateDir, legacyPath) {
		return nil
	}
	resolvedStateDir, err := filepath.EvalSymlinks(stateDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve canonical state directory: %w", err)
	}
	resolvedLegacyDir, err := filepath.EvalSymlinks(filepath.Dir(legacyPath))
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("resolve legacy state directory: %w", err)
	}
	if !pathWithinDirectory(resolvedStateDir, resolvedLegacyDir) {
		return nil
	}

	info, err := os.Lstat(legacyPath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("inspect legacy state path: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return nil
	}

	legacyState, err := statepkg.LoadState(legacyPath)
	if err != nil {
		return fmt.Errorf("load legacy state file: %w", err)
	}
	if err := legacyState.Save(canonicalPath); err != nil {
		return fmt.Errorf("save migrated state file: %w", err)
	}

	backupPath := legacyPath + ".migrated"
	if _, err := os.Lstat(backupPath); err == nil {
		if s.logger != nil {
			s.logger.Warn("Migrated legacy sync state while preserving the existing backup", map[string]interface{}{
				"profileID": profileID,
				"path":      legacyPath,
			})
		}
		return nil
	} else if !os.IsNotExist(err) {
		if s.logger != nil {
			s.logger.Warn("Migrated legacy sync state but could not inspect its backup path", map[string]interface{}{
				"profileID": profileID,
				"path":      legacyPath,
				"error":     err.Error(),
			})
		}
		return nil
	}
	if err := os.Rename(legacyPath, backupPath); err != nil && s.logger != nil {
		s.logger.Warn("Migrated legacy sync state but could not rename the original", map[string]interface{}{
			"profileID": profileID,
			"path":      legacyPath,
			"error":     err.Error(),
		})
	}
	return nil
}

func pathWithinDirectory(directory, candidate string) bool {
	relative, err := filepath.Rel(directory, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

// pathWithinResolvedDirectory checks containment after resolving every
// existing component. The final state file (and any newly-created suffix) is
// allowed not to exist yet, while an existing symlinked parent is still
// accounted for.
func pathWithinResolvedDirectory(directory, candidate string) (bool, error) {
	resolvedDirectory, err := resolvePathWithExistingComponents(directory)
	if err != nil {
		return false, fmt.Errorf("resolve directory: %w", err)
	}
	resolvedCandidate, err := resolvePathWithExistingComponents(candidate)
	if err != nil {
		return false, fmt.Errorf("resolve candidate: %w", err)
	}
	return pathWithinDirectory(resolvedDirectory, resolvedCandidate), nil
}

// resolvePathWithExistingComponents resolves the existing prefix of a path,
// then appends its possibly-missing suffix. This avoids requiring the state
// file or its new parent directories to exist before validation.
func resolvePathWithExistingComponents(path string) (string, error) {
	absPath, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}

	current := absPath
	missing := make([]string, 0)
	for {
		if _, err := os.Lstat(current); err == nil {
			resolved, err := filepath.EvalSymlinks(current)
			if err != nil {
				return "", err
			}
			for index := len(missing) - 1; index >= 0; index-- {
				resolved = filepath.Join(resolved, missing[index])
			}
			return resolved, nil
		} else if !os.IsNotExist(err) && !errors.Is(err, syscall.ENAMETOOLONG) {
			return "", err
		}

		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", path)
		}
		missing = append(missing, filepath.Base(current))
		current = parent
	}
}

// safeLegacyProfileIDPathSegments permits historical IDs containing directory
// separators, but rejects empty and dot path components before the raw path is
// constructed. Otherwise a value such as "foo/../other" could alias another
// profile's legacy filename after filepath.Abs cleans it.
func safeLegacyProfileIDPathSegments(profileID string) bool {
	if profileID == "" || strings.ContainsRune(profileID, '\x00') {
		return false
	}

	segmentStart := 0
	for index := 0; index < len(profileID); index++ {
		if !os.IsPathSeparator(profileID[index]) {
			continue
		}
		segment := profileID[segmentStart:index]
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
		segmentStart = index + 1
	}

	segment := profileID[segmentStart:]
	return segment != "" && segment != "." && segment != ".."
}

func validateStatePathComponentLengths(relativePath string) error {
	for _, component := range strings.Split(filepath.Clean(relativePath), string(filepath.Separator)) {
		if len([]byte(component)) > maxStateFileComponentBytes {
			return fmt.Errorf("%w: %q", ErrProfileStateFileNameTooLong, component)
		}
	}
	return nil
}

func (s *MultiUserService) validateProfileStateFile(profileID, configuredPath string) error {
	return s.validateProfileStateFileWithAbsolutePolicy(profileID, configuredPath, false)
}

func (s *MultiUserService) validatePersistedProfileStateFile(profileID, configuredPath string) error {
	return s.validateProfileStateFileWithAbsolutePolicy(profileID, configuredPath, true)
}

func (s *MultiUserService) validateProfileStateFileWithAbsolutePolicy(profileID, configuredPath string, allowAbsolute bool) error {
	dataDir, err := filepath.Abs(s.effectiveDataDir())
	if err != nil {
		return fmt.Errorf("%w: resolve data directory: %v", ErrProfileStateFilePathNotAllowed, err)
	}

	if configuredPath != "" {
		if strings.ContainsRune(configuredPath, '\x00') {
			return fmt.Errorf("%w: NUL bytes are not accepted: %q", ErrProfileStateFilePathNotAllowed, configuredPath)
		}
		if filepath.IsAbs(configuredPath) && !allowAbsolute {
			return fmt.Errorf("%w: absolute paths are not accepted: %q", ErrProfileStateFilePathNotAllowed, configuredPath)
		}

		configured := configuredPath
		if !filepath.IsAbs(configured) {
			configured = filepath.Join(dataDir, configured)
		}
		configured, err = filepath.Abs(configured)
		if err != nil {
			return fmt.Errorf("%w: resolve path: %v", ErrProfileStateFilePathNotAllowed, err)
		}
		if !pathWithinDirectory(dataDir, configured) {
			return fmt.Errorf("%w: path escapes data directory: %q", ErrProfileStateFilePathNotAllowed, configuredPath)
		}
		withinResolvedDataDir, err := pathWithinResolvedDirectory(dataDir, configured)
		if err != nil {
			return fmt.Errorf("%w: resolve configured path: %v", ErrProfileStateFilePathNotAllowed, err)
		}
		if !withinResolvedDataDir {
			return fmt.Errorf("%w: resolved path escapes data directory: %q", ErrProfileStateFilePathNotAllowed, configuredPath)
		}
	}

	derivedPath := s.profileSpecificStatePath(profileID, configuredPath)
	derived, err := filepath.Abs(derivedPath)
	if err != nil {
		return fmt.Errorf("%w: resolve derived path: %v", ErrProfileStateFilePathNotAllowed, err)
	}
	if !pathWithinDirectory(dataDir, derived) {
		return fmt.Errorf("%w: derived path escapes data directory: %q", ErrProfileStateFilePathNotAllowed, configuredPath)
	}
	derivedRelative, err := filepath.Rel(dataDir, derived)
	if err != nil {
		return fmt.Errorf("%w: compare derived path with data directory: %v", ErrProfileStateFilePathNotAllowed, err)
	}
	if err := validateStatePathComponentLengths(derivedRelative); err != nil {
		return err
	}
	withinResolvedDataDir, err := pathWithinResolvedDirectory(dataDir, derivedPath)
	if err != nil {
		return fmt.Errorf("%w: resolve derived path: %v", ErrProfileStateFilePathNotAllowed, err)
	}
	if !withinResolvedDataDir {
		return fmt.Errorf("%w: resolved derived path escapes data directory: %q", ErrProfileStateFilePathNotAllowed, configuredPath)
	}
	return nil
}

func (s *MultiUserService) effectiveDataDir() string {
	if s.globalConfig != nil && s.globalConfig.Paths.DataDir != "" {
		return s.globalConfig.Paths.DataDir
	}
	return "/data"
}

// encodeProfileID turns every non-unreserved byte into a percent-encoded
// sequence so even legacy IDs containing route delimiters remain one filename
// component. New IDs are already restricted to the unreserved set.
func encodeProfileID(profileID string) string {
	const hex = "0123456789ABCDEF"
	var encoded strings.Builder
	encoded.Grow(len(profileID))
	for i := 0; i < len(profileID); i++ {
		char := profileID[i]
		if (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z') ||
			(char >= '0' && char <= '9') || char == '-' || char == '.' || char == '_' || char == '~' {
			encoded.WriteByte(char)
			continue
		}
		encoded.WriteByte('%')
		encoded.WriteByte(hex[char>>4])
		encoded.WriteByte(hex[char&0x0f])
	}
	return encoded.String()
}

func cloneSyncSnapshot(snapshot sync.SyncSnapshot) *sync.SyncSnapshot {
	copyOf := snapshot
	copyOf.BookOutcomes = append([]sync.BookOutcomeRecord(nil), snapshot.BookOutcomes...)
	return &copyOf
}

func applySnapshotToStatus(status *SyncProfileStatus, snapshot sync.SyncSnapshot) {
	if status == nil {
		return
	}
	status.Snapshot = cloneSyncSnapshot(snapshot)
}

func cloneProfileStatus(status *SyncProfileStatus) *SyncProfileStatus {
	if status == nil {
		return nil
	}
	copyOf := *status
	if status.LastAttemptedAt != nil {
		lastAttempted := *status.LastAttemptedAt
		copyOf.LastAttemptedAt = &lastAttempted
	}
	if status.LastSuccessfulAt != nil {
		lastSuccessful := *status.LastSuccessfulAt
		copyOf.LastSuccessfulAt = &lastSuccessful
	}
	if status.Snapshot != nil {
		copyOf.Snapshot = cloneSyncSnapshot(*status.Snapshot)
	}
	return &copyOf
}

// updateProfileStatus updates the status for a profile
func (s *MultiUserService) updateProfileStatus(profileID string, status *SyncProfileStatus) {
	s.statusMutex.Lock()
	defer s.statusMutex.Unlock()
	s.profileStatuses[profileID] = cloneProfileStatus(status)
}
