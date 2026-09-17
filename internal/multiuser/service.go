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
	"github.com/drallgood/audiobookshelf-hardcover-sync/internal/mismatch"
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

// SyncProfileStatus represents the sync status for a profile
type SyncProfileStatus struct {
	ProfileID        string                  `json:"profile_id"`
	ProfileName      string                  `json:"profile_name"`
	Status           string                  `json:"status"` // "idle", "syncing", "error", "completed"
	DryRun           bool                    `json:"dry_run,omitempty"`
	LastSync         *time.Time              `json:"last_sync"`
	LastAttemptedAt  *time.Time              `json:"last_attempted_at,omitempty"`
	LastSuccessfulAt *time.Time              `json:"last_successful_at,omitempty"`
	Error            string                  `json:"error,omitempty"`
	Progress         string                  `json:"progress,omitempty"`
	BooksTotal       int                     `json:"books_total,omitempty"`
	BooksSynced      int                     `json:"books_synced,omitempty"`
	BooksNotFound    []sync.BookNotFoundInfo `json:"books_not_found,omitempty"`
	Mismatches       []mismatch.BookMismatch `json:"mismatches,omitempty"`
	LastSyncSummary  *sync.SyncSummary       `json:"last_sync_summary,omitempty"`
	Snapshot         *sync.SyncSnapshot      `json:"snapshot,omitempty"`
}

// AcceptedSyncRun is the immutable identity returned for a successfully
// accepted start. Its value fields are copied from the durable queued
// reservation before the sync worker is started, so callers never need to
// infer the accepted run from a later status read.
type AcceptedSyncRun struct {
	RunID        string    `json:"run_id"`
	QueuedAt     time.Time `json:"queued_at"`
	RunStartedAt time.Time `json:"run_started_at"`
	State        string    `json:"state"`
	DryRun       bool      `json:"dry_run"`
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
// gate is held, but they never hold the service-wide map lock.
type profileRunGate struct {
	mu stdSync.Mutex
}

// MultiUserService manages sync operations for multiple users
type MultiUserService struct {
	repository      *database.Repository
	logger          *logger.Logger
	globalConfig    *config.Config
	profileStatuses map[string]*SyncProfileStatus
	statusMutex     stdSync.RWMutex
	activeSyncs     map[string]context.CancelFunc
	activeRuns      map[string]activeSyncRun
	nextGeneration  uint64
	syncMutex       stdSync.RWMutex
	syncWaitGroup   stdSync.WaitGroup
	syncServices    map[string]*sync.Service // Maps profile ID to its sync service
	serviceRuns     map[string]uint64
	latestRuns      map[string]activeSyncRun
	profileGates    map[string]*profileRunGate
	servicesMutex   stdSync.RWMutex
}

// NewMultiUserService creates a new multi-user service
func NewMultiUserService(repo *database.Repository, globalConfig *config.Config, log *logger.Logger) *MultiUserService {
	return &MultiUserService{
		repository:      repo,
		logger:          log,
		globalConfig:    globalConfig,
		profileStatuses: make(map[string]*SyncProfileStatus),
		activeSyncs:     make(map[string]context.CancelFunc),
		activeRuns:      make(map[string]activeSyncRun),
		syncServices:    make(map[string]*sync.Service),
		serviceRuns:     make(map[string]uint64),
		latestRuns:      make(map[string]activeSyncRun),
		profileGates:    make(map[string]*profileRunGate),
	}
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
	return s.repository.CreateProfileForUser(profileID, name, audiobookshelfURL, audiobookshelfToken, hardcoverToken, syncConfig, ownerUserID)
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
	// Cancel any active sync for this profile
	if err := s.CancelSync(profileID); err != nil {
		s.logger.Warn("Failed to cancel sync during profile deletion", map[string]interface{}{
			"profileID": profileID,
			"error":     err,
		})
	}

	// Remove from status tracking
	s.statusMutex.Lock()
	delete(s.profileStatuses, profileID)
	s.statusMutex.Unlock()

	return s.repository.DeleteProfile(profileID)
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
// remain visible. This path deliberately avoids getProfileStatus because that
// endpoint needs the full snapshot and legacy detail arrays.
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
		snapshot.UserID = profile.ID
		snapshot.RunID = run.runID
		snapshot.RunStartedAt = run.startedAt
		if snapshot.State == "" || snapshot.State == "idle" {
			snapshot.State = "syncing"
		}
	}
	s.syncMutex.RUnlock()
	if status == nil {
		status = &SyncProfileStatus{
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
			Status:      "syncing",
		}
	}
	if status.ProfileID == "" {
		status.ProfileID = profile.ID
	}
	if status.ProfileName == "" {
		status.ProfileName = profile.Name
	}
	status.Status = statusForSnapshot(&snapshot)
	// Aggregate callers consume the canonical lifecycle phase directly. Keep
	// the legacy "syncing" projection confined to GetProfileStatus, whose
	// compatibility payload is replaced with a full canonical snapshot by the
	// per-profile status handler.
	status.Snapshot = &snapshot
	status.BooksTotal = int(snapshot.BooksTotal)
	status.BooksSynced = int(snapshot.BooksSynced)

	return aggregateProfileStatus(profile, status)
}

// getStoredAggregateStatus copies only the scalar fields needed by aggregate
// polling. In particular, terminal snapshots are reduced without copying
// their per-book outcomes, attention records, not-found entries, or mismatches.
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
		Status:      stored.Status,
		DryRun:      stored.DryRun,
		Progress:    stored.Progress,
		BooksTotal:  stored.BooksTotal,
		BooksSynced: stored.BooksSynced,
	}
	if stored.LastSync != nil {
		lastSync := *stored.LastSync
		status.LastSync = &lastSync
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
// per-profile status endpoint.
func aggregateProfileStatus(profile database.SyncProfile, status *SyncProfileStatus) *SyncProfileStatus {
	if status == nil {
		return &SyncProfileStatus{
			ProfileID:   profile.ID,
			ProfileName: profile.Name,
			Status:      "idle",
		}
	}

	aggregate := &SyncProfileStatus{
		ProfileID:        status.ProfileID,
		ProfileName:      status.ProfileName,
		Status:           status.Status,
		DryRun:           status.DryRun,
		LastSync:         status.LastSync,
		LastAttemptedAt:  status.LastAttemptedAt,
		LastSuccessfulAt: status.LastSuccessfulAt,
		Progress:         status.Progress,
		BooksTotal:       status.BooksTotal,
		BooksSynced:      status.BooksSynced,
	}
	aggregate.Snapshot = status.Snapshot
	if aggregate.Status == "" {
		aggregate.Status = "idle"
	}
	return aggregate
}

// scalarSnapshot keeps aggregate polling cheap and prevents the public
// /api/status response from copying per-book outcome and attention records.
// The full snapshot remains available on the authenticated profile status and
// run-details endpoints.
func scalarSnapshot(snapshot *sync.SyncSnapshot) *sync.SyncSnapshot {
	if snapshot == nil {
		return nil
	}
	return &sync.SyncSnapshot{
		UserID:              snapshot.UserID,
		RunID:               snapshot.RunID,
		RunStartedAt:        snapshot.RunStartedAt,
		QueuedAt:            snapshot.QueuedAt,
		ProcessingStartedAt: snapshot.ProcessingStartedAt,
		LastActivityAt:      snapshot.LastActivityAt,
		LastProcessedAt:     snapshot.LastProcessedAt,
		FinishedAt:          snapshot.FinishedAt,
		DryRun:              snapshot.DryRun,
		State:               snapshot.State,
		BooksTotal:          snapshot.BooksTotal,
		ProcessedSoFar:      snapshot.ProcessedSoFar,
		ProcessedCount:      snapshot.ProcessedCount,
		OutcomeCounts:       snapshot.OutcomeCounts,
		TotalBooksProcessed: snapshot.TotalBooksProcessed,
		BooksSynced:         snapshot.BooksSynced,
	}
}

func (s *MultiUserService) profileGate(profileID string) *profileRunGate {
	s.syncMutex.Lock()
	defer s.syncMutex.Unlock()
	if gate := s.profileGates[profileID]; gate != nil {
		return gate
	}
	gate := &profileRunGate{}
	s.profileGates[profileID] = gate
	return gate
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

func statusForSnapshot(snapshot *sync.SyncSnapshot) string {
	if snapshot == nil {
		return "idle"
	}
	switch snapshot.State {
	case string(sync.RunPhaseCompleted):
		return "completed"
	case string(sync.RunPhaseCanceled), string(sync.RunPhaseFailed):
		return "error"
	case string(sync.RunPhaseQueued), string(sync.RunPhaseRunning), string(sync.RunPhaseFinalizing):
		return "syncing"
	default:
		return "syncing"
	}
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
	copyOf.AttentionRecords = append([]sync.BookOutcomeRecord(nil), snapshot.AttentionRecords...)
	copyOf.BooksNotFound = append([]sync.BookNotFoundInfo(nil), snapshot.BooksNotFound...)
	copyOf.Mismatches = make([]mismatch.BookMismatch, len(snapshot.Mismatches))
	for i, record := range snapshot.Mismatches {
		copyOf.Mismatches[i] = cloneBookMismatch(record)
	}
	for i := range copyOf.BookOutcomes {
		copyOf.BookOutcomes[i].CoverURL = sanitizeReportURL(copyOf.BookOutcomes[i].CoverURL)
	}
	for i := range copyOf.AttentionRecords {
		copyOf.AttentionRecords[i].CoverURL = sanitizeReportURL(copyOf.AttentionRecords[i].CoverURL)
	}
	for i := range copyOf.Mismatches {
		copyOf.Mismatches[i].CoverURL = sanitizeReportURL(copyOf.Mismatches[i].CoverURL)
		copyOf.Mismatches[i].ImageURL = sanitizeReportURL(copyOf.Mismatches[i].ImageURL)
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
		ReportVersion:       database.SyncRunReportVersion,
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

func (s *MultiUserService) restoreLatestTerminalSnapshot(profileID string) (*sync.SyncSnapshot, error) {
	if s.repository == nil {
		return nil, nil
	}
	reports, err := s.repository.ListTerminalSyncRunReports(profileID, 0)
	if err != nil {
		return nil, err
	}
	for index := range reports {
		if terminalReportPhase(reports[index].Phase) {
			return snapshotFromRetainedReport(profileID, &reports[index])
		}
	}
	return nil, nil
}

// restoreProfileStatus rebuilds a non-active status from the newest retained
// terminal report. Queued/processing reports can be left behind by a process
// restart and must never be presented as an active run.
func (s *MultiUserService) restoreProfileStatus(profileID string, profile *database.SyncProfile) (*SyncProfileStatus, error) {
	if s.repository == nil {
		return nil, nil
	}
	if profile == nil {
		var err error
		profile, err = s.repository.GetProfileMetadata(profileID)
		if err != nil || profile == nil {
			return nil, err
		}
	}
	state, err := s.repository.GetProfileSyncState(profileID)
	if err != nil {
		return nil, err
	}
	reports, err := s.repository.ListTerminalSyncRunReports(profileID, 0)
	if err != nil {
		return nil, err
	}
	var report *database.SyncRunReport
	for index := range reports {
		if terminalReportPhase(reports[index].Phase) {
			report = &reports[index]
			break
		}
	}

	if profile == nil {
		return nil, nil
	}

	status := &SyncProfileStatus{
		ProfileID:   profile.ID,
		ProfileName: profile.Name,
		Status:      "idle",
	}
	if state != nil {
		status.LastSync = copyTime(state.LastSync)
		status.LastAttemptedAt = copyTime(state.LastAttemptedAt)
		status.LastSuccessfulAt = copyTime(state.LastSuccessfulAt)
		if status.LastAttemptedAt == nil {
			// Legacy state only has LastSync. It is an attempted timestamp,
			// not evidence of successful synchronization.
			status.LastAttemptedAt = copyTime(state.LastSync)
		}
	}
	if report == nil {
		return status, nil
	}

	snapshot, err := snapshotFromRetainedReport(profile.ID, report)
	if err != nil {
		return nil, err
	}
	if snapshot == nil {
		return status, nil
	}
	applySnapshotToStatus(status, *snapshot)
	status.Status = statusForSnapshot(snapshot)
	if snapshot.State == string(sync.RunPhaseFailed) {
		status.Error = snapshot.RunError
	}
	if status.LastAttemptedAt == nil {
		status.LastAttemptedAt = copyTime(report.QueuedAt)
	}
	if report.Phase == database.SyncRunPhaseCompleted && !report.DryRun && status.LastSuccessfulAt == nil {
		status.LastSuccessfulAt = copyTime(report.FinishedAt)
		status.LastSync = copyTime(report.FinishedAt)
	}
	return status, nil
}

// restoreAggregateProfileStatus rebuilds only the scalar status needed by
// aggregate polling. Retained per-book arrays are deliberately excluded from
// SnapshotJSON decoding; authenticated profile status restores the full report
// through restoreProfileStatus instead.
func (s *MultiUserService) restoreAggregateProfileStatus(profileID string, profile *database.SyncProfile) (*SyncProfileStatus, error) {
	if s.repository == nil {
		return nil, nil
	}
	state, err := s.repository.GetProfileSyncState(profileID)
	if err != nil {
		return nil, err
	}
	reports, err := s.repository.ListTerminalSyncRunReports(profileID, 0)
	if err != nil {
		return nil, err
	}

	status := &SyncProfileStatus{ProfileID: profileID, Status: "idle"}
	if profile != nil {
		status.ProfileName = profile.Name
	}
	if state != nil {
		status.LastSync = copyTime(state.LastSync)
		status.LastAttemptedAt = copyTime(state.LastAttemptedAt)
		status.LastSuccessfulAt = copyTime(state.LastSuccessfulAt)
		if status.LastAttemptedAt == nil {
			status.LastAttemptedAt = copyTime(state.LastSync)
		}
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
	status.Status = statusForSnapshot(snapshot)
	status.DryRun = snapshot.DryRun
	status.BooksTotal = int(snapshot.BooksTotal)
	status.BooksSynced = int(snapshot.BooksSynced)
	if status.LastAttemptedAt == nil {
		status.LastAttemptedAt = copyTime(report.QueuedAt)
	}
	if report.Phase == database.SyncRunPhaseCompleted && !report.DryRun && status.LastSuccessfulAt == nil {
		status.LastSuccessfulAt = copyTime(report.FinishedAt)
		status.LastSync = copyTime(report.FinishedAt)
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
		BooksTotal          int32              `json:"books_total"`
		ProcessedSoFar      int32              `json:"processed_so_far"`
		ProcessedCount      int32              `json:"processed_count"`
		UnattemptedCount    int32              `json:"unattempted_count"`
		OutcomeCounts       sync.OutcomeCounts `json:"outcome_counts"`
		TotalBooksProcessed int32              `json:"total_books_processed"`
		BooksSynced         int32              `json:"books_synced"`
	}
	if report.SnapshotJSON != "" && report.SnapshotJSON != "{}" {
		if err := json.Unmarshal([]byte(report.SnapshotJSON), &scalar); err != nil {
			return nil, fmt.Errorf("decode retained sync report %s: %w", report.RunID, err)
		}
	}
	snapshot := &sync.SyncSnapshot{
		UserID: profileID, RunID: report.RunID, State: reportPhaseToSyncPhase(report.Phase),
		DryRun: report.DryRun, RunError: report.RunError,
		UnattemptedCount: scalar.UnattemptedCount, BooksTotal: scalar.BooksTotal,
		ProcessedSoFar: scalar.ProcessedSoFar,
		ProcessedCount: scalar.ProcessedCount, OutcomeCounts: scalar.OutcomeCounts,
		TotalBooksProcessed: scalar.TotalBooksProcessed, BooksSynced: scalar.BooksSynced,
	}
	if report.QueuedAt != nil {
		snapshot.QueuedAt = report.QueuedAt.UTC()
		snapshot.RunStartedAt = snapshot.QueuedAt
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
	snapshot.UserID = profileID
	snapshot.RunID = report.RunID
	snapshot.State = reportPhaseToSyncPhase(report.Phase)
	snapshot.DryRun = report.DryRun
	if report.QueuedAt != nil {
		snapshot.QueuedAt = report.QueuedAt.UTC()
		snapshot.RunStartedAt = snapshot.QueuedAt
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

// legacySnapshotState retains the pre-lifecycle status payload spelling for
// active runs. The canonical phase remains in durable reports and direct
// snapshot accessors; only the legacy profile-status projection uses this.
func legacySnapshotState(state string) string {
	switch state {
	case string(sync.RunPhaseQueued), string(sync.RunPhaseRunning), string(sync.RunPhaseFinalizing):
		return "syncing"
	default:
		return state
	}
}

// GetSyncService returns the sync service for a profile, if it exists
func (s *MultiUserService) GetSyncService(profileID string) (*sync.Service, bool) {
	service, _ := s.currentSyncService(profileID)
	return service, service != nil
}

// GetProfileStatus returns the sync status for a profile
func (s *MultiUserService) GetProfileStatus(profileID string) *SyncProfileStatus {
	return s.getProfileStatus(profileID, nil, nil)
}

// GetProfileSnapshot returns the current in-memory run snapshot for a profile
// without hydrating profile configuration from the repository. It is intended
// for callers that have already performed any required profile authorization.
func (s *MultiUserService) GetProfileSnapshot(profileID string) *sync.SyncSnapshot {
	s.statusMutex.RLock()
	var storedSnapshot *sync.SyncSnapshot
	if status := s.profileStatuses[profileID]; status != nil && status.Snapshot != nil {
		storedSnapshot = cloneSyncSnapshot(*status.Snapshot)
	}
	s.statusMutex.RUnlock()
	s.syncMutex.RLock()
	service, generation := s.currentSyncServiceLocked(profileID)
	if service == nil {
		s.syncMutex.RUnlock()
		if storedSnapshot == nil {
			if restored, err := s.restoreLatestTerminalSnapshot(profileID); err == nil {
				storedSnapshot = restored
			}
		}
		return storedSnapshot
	}

	snapshot := service.GetSnapshot()
	if generation > 0 {
		if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
			snapshot.UserID = profileID
			snapshot.RunID = run.runID
			snapshot.RunStartedAt = run.startedAt
			if snapshot.State == "" || snapshot.State == "idle" {
				snapshot.State = "syncing"
			}
		}
	}
	if snapshot.UserID == "" {
		snapshot.UserID = profileID
	}

	if storedSnapshot == nil || storedSnapshot.RunID == "" || snapshot.RunID == "" || storedSnapshot.RunID == snapshot.RunID {
		s.syncMutex.RUnlock()
		return &snapshot
	}
	s.syncMutex.RUnlock()
	return storedSnapshot
}

// GetActiveOrLatestSnapshot returns the active run snapshot when one exists,
// otherwise the newest retained terminal report restored from the database.
func (s *MultiUserService) GetActiveOrLatestSnapshot(profileID string) *sync.SyncSnapshot {
	return s.GetProfileSnapshot(profileID)
}

// GetSyncRunReport returns an exact profile-scoped retained report. It does
// not fall back to the profile's latest report when runID is unknown.
func (s *MultiUserService) GetSyncRunReport(profileID, runID string) (*database.SyncRunReport, error) {
	if s.repository == nil {
		return nil, nil
	}
	return s.repository.GetSyncRunReport(profileID, runID)
}

// GetRetainedSyncRunSnapshot returns the exact retained snapshot for runID.
// Active snapshots are intentionally not synthesized here; callers that need
// the live run should use GetActiveOrLatestSnapshot.
func (s *MultiUserService) GetRetainedSyncRunSnapshot(profileID, runID string) (*sync.SyncSnapshot, error) {
	report, err := s.GetSyncRunReport(profileID, runID)
	if err != nil || report == nil {
		return nil, err
	}
	if !terminalReportPhase(report.Phase) {
		return nil, nil
	}
	return snapshotFromRetainedReport(profileID, report)
}

// GetSyncRunSnapshot is a concise alias for exact retained run lookup.
func (s *MultiUserService) GetSyncRunSnapshot(profileID, runID string) (*sync.SyncSnapshot, error) {
	return s.GetRetainedSyncRunSnapshot(profileID, runID)
}

// currentSyncService returns only the service belonging to the active run.
// A stale goroutine may remain briefly during cleanup, so its service must not
// be exposed after a replacement run has started.
func (s *MultiUserService) currentSyncService(profileID string) (*sync.Service, uint64) {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()
	return s.currentSyncServiceLocked(profileID)
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

func (s *MultiUserService) getProfileStatus(profileID string, profile *database.SyncProfile, profileState *database.ProfileSyncState) *SyncProfileStatus {
	// A status lookup can hydrate its fallback from the database. Do that
	// without holding syncMutex so a slow database cannot block a new run.
	s.statusMutex.RLock()
	status := cloneProfileStatus(s.profileStatuses[profileID])
	s.statusMutex.RUnlock()

	if status == nil {
		if restored, err := s.restoreProfileStatus(profileID, profile); err == nil && restored != nil {
			status = s.cacheStatusIfAbsent(profileID, restored)
		}
	}
	if status == nil {
		if profile == nil {
			if s.repository != nil {
				loaded, _ := s.GetProfile(profileID)
				if loaded != nil {
					profile = &loaded.Profile
					profileState = loaded.Profile.SyncState
				}
			}
		}
	}
	if (status == nil || status.LastSync == nil || status.LastAttemptedAt == nil || status.LastSuccessfulAt == nil) && profileState == nil &&
		s.repository != nil && (status != nil || profile != nil) {
		profileState, _ = s.repository.GetSyncState(profileID)
	}

	// Re-read status and service state together after fallback I/O. A run can
	// start while either lookup is in progress, and its status must win over a
	// stale loaded fallback.
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()

	s.statusMutex.RLock()
	status = cloneProfileStatus(s.profileStatuses[profileID])
	s.statusMutex.RUnlock()
	if status == nil {
		if profile == nil {
			return &SyncProfileStatus{ProfileID: profileID, Status: "error", Error: "Profile not found"}
		}
		status = &SyncProfileStatus{ProfileID: profileID, ProfileName: profile.Name, Status: "idle"}
	}
	if status.Status == "" {
		status.Status = "idle"
	}
	if status.ProfileName == "" && profile != nil {
		status.ProfileName = profile.Name
	}
	if status.LastSync == nil && profileState != nil && profileState.LastSync != nil {
		lastSync := *profileState.LastSync
		status.LastSync = &lastSync
	}
	if status.LastAttemptedAt == nil && profileState != nil && profileState.LastAttemptedAt != nil {
		lastAttempted := *profileState.LastAttemptedAt
		status.LastAttemptedAt = &lastAttempted
	}
	if status.LastSuccessfulAt == nil && profileState != nil && profileState.LastSuccessfulAt != nil {
		lastSuccessful := *profileState.LastSuccessfulAt
		status.LastSuccessfulAt = &lastSuccessful
	}
	service, generation := s.currentSyncServiceLocked(profileID)
	if service == nil {
		if status.Status == "" {
			status.Status = "idle"
		}
		return status
	}
	snapshot := service.GetSnapshot()
	if generation > 0 {
		if run, ok := s.activeRuns[profileID]; ok && run.generation == generation {
			snapshot.UserID = profileID
			snapshot.RunID = run.runID
			snapshot.RunStartedAt = run.startedAt
			if snapshot.State == "" || snapshot.State == "idle" {
				snapshot.State = "syncing"
			}
		}
	}
	projectedSnapshot := snapshot
	projectedSnapshot.State = legacySnapshotState(snapshot.State)
	if status.Snapshot == nil || status.Snapshot.RunID == "" || snapshot.RunID == "" || status.Snapshot.RunID == snapshot.RunID {
		applySnapshotToStatus(status, projectedSnapshot)
		if status.Snapshot.UserID == "" {
			status.Snapshot.UserID = profileID
		}
	}
	status.Status = statusForSnapshot(status.Snapshot)
	return status
}

// StartSync starts a sync operation for a specific profile. It retains the
// legacy error-only contract; callers that need the accepted identity should
// use StartSyncWithAcceptedRun.
func (s *MultiUserService) StartSync(profileID string) error {
	_, err := s.StartSyncWithAcceptedRun(profileID)
	return err
}

// StartSyncWithAcceptedRun starts a sync and returns the durable queued run
// identity accepted for it. The returned record is constructed from the same
// reservation that installed the queued report, before its worker is started.
func (s *MultiUserService) StartSyncWithAcceptedRun(profileID string) (AcceptedSyncRun, error) {
	gate := s.profileGate(profileID)
	gate.mu.Lock()
	defer gate.mu.Unlock()

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
		ReportVersion: database.SyncRunReportVersion, SnapshotJSON: queuedSnapshotJSON,
	})
	if err != nil {
		return AcceptedSyncRun{}, fmt.Errorf("failed to accept sync run for profile %s: %w", profileID, err)
	}
	if queuedReport == nil || queuedReport.QueuedAt == nil {
		return AcceptedSyncRun{}, fmt.Errorf("failed to accept sync run for profile %s: incomplete acceptance", profileID)
	}
	accepted := AcceptedSyncRun{
		RunID:        queuedReport.RunID,
		QueuedAt:     queuedReport.QueuedAt.UTC(),
		RunStartedAt: queuedReport.QueuedAt.UTC(),
		State:        string(sync.RunPhaseQueued),
		DryRun:       queuedReport.DryRun,
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
	if run.generation > s.nextGeneration {
		s.nextGeneration = run.generation
	}
	s.syncMutex.Unlock()

	// Update initial status
	lastSync, lastSuccessful := s.priorRunTimes(profileID, profileConfig.Profile.SyncState)
	initialStatus := &SyncProfileStatus{
		ProfileID:        profileID,
		ProfileName:      profileConfig.Profile.Name,
		Status:           "syncing",
		DryRun:           accepted.DryRun,
		LastSync:         lastSync,
		LastAttemptedAt:  timeValue(accepted.QueuedAt),
		LastSuccessfulAt: lastSuccessful,
		Progress:         "Starting sync...",
	}
	applySnapshotToStatus(initialStatus, queuedSnapshot)
	s.updateProfileStatus(profileID, initialStatus)

	// Start the sync in background
	s.syncWaitGroup.Add(1)
	go func() {
		defer s.syncWaitGroup.Done()
		s.performSync(ctx, profileID, profileConfig, run.generation)
	}()
	return accepted, nil
}

// WaitForSyncs waits for all sync goroutines started by this service to exit.
// It is used by lifecycle owners that must release resources, such as the
// logger and test databases, only after asynchronous work has stopped.
func (s *MultiUserService) WaitForSyncs() {
	s.syncWaitGroup.Wait()
}

// CancelSync cancels a running sync operation for a profile.
func (s *MultiUserService) CancelSync(profileID string) error {
	gate := s.profileGate(profileID)
	gate.mu.Lock()
	defer gate.mu.Unlock()

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
	finalStatus.Status = "error"
	finalStatus.Progress = "Sync canceled"
	if err := s.persistTerminalSnapshot(profileID, run.generation, snapshot); err != nil {
		finalStatus.Error = fmt.Sprintf("failed to persist canceled sync report: %v", err)
		snapshot.State = string(sync.RunPhaseFailed)
		snapshot.RunError = finalStatus.Error
		applySnapshotToStatus(finalStatus, snapshot)
		finalStatus.Status = "error"
	}
	s.publishStatusIfLatest(profileID, run, finalStatus)
	return nil
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
			Status:      "error",
			Error:       fmt.Sprintf("Failed to migrate legacy sync state: %v", err),
		}
		if run, ok := s.activeRun(profileID, generation); ok {
			applySnapshotToStatus(status, newRunSnapshot(profileID, run, "failed"))
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
			Status:      "error",
			Error:       fmt.Sprintf("Failed to create sync service: %v", err),
		}
		if run, ok := s.activeRun(profileID, generation); ok {
			applySnapshotToStatus(status, newRunSnapshot(profileID, run, "failed"))
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

	// Obtain summary
	summary := syncService.GetSummary()
	snapshot := s.normalizeRunSnapshot(profileID, generation, syncService.GetSnapshot())

	// Prepare final status
	status := s.statusForTerminalRun(profileID, run, snapshot)
	applySnapshotToStatus(status, snapshot)
	if status.Snapshot != nil && status.Snapshot.UserID == "" {
		status.Snapshot.UserID = profileID
	}

	if err != nil {
		status.Status = "error"
		status.Error = err.Error()
		s.logger.Error("Sync failed", map[string]interface{}{
			"profileID": profileID,
			"error":     err,
		})
	} else {
		status.Status = "completed"
		status.Progress = "Sync completed successfully"

		s.logger.Debug("Stored full sync summary in profile status", map[string]interface{}{
			"profileID":       profileID,
			"books_processed": summary.TotalBooksProcessed,
			"books_synced":    summary.BooksSynced,
			"books_not_found": len(summary.BooksNotFound),
			"mismatches":      len(summary.Mismatches),
		})
	}

	// Persist the terminal report without holding the service-wide lifecycle
	// lock, then publish in memory only if run ID and generation are current.
	s.publishFinalStatus(profileID, generation, status)
}

func newRunSnapshot(profileID string, run activeSyncRun, state string) sync.SyncSnapshot {
	snapshot := sync.SyncSnapshot{
		UserID:           profileID,
		RunID:            run.runID,
		RunStartedAt:     run.startedAt,
		QueuedAt:         run.startedAt,
		LastActivityAt:   run.startedAt,
		State:            state,
		DryRun:           run.dryRun,
		BookOutcomes:     make([]sync.BookOutcomeRecord, 0),
		AttentionRecords: make([]sync.BookOutcomeRecord, 0),
		BooksNotFound:    make([]sync.BookNotFoundInfo, 0),
		Mismatches:       make([]mismatch.BookMismatch, 0),
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

func (s *MultiUserService) priorRunTimes(profileID string, profileState *database.ProfileSyncState) (*time.Time, *time.Time) {
	s.statusMutex.RLock()
	stored := s.profileStatuses[profileID]
	var lastSync, lastSuccessful *time.Time
	if stored != nil {
		lastSync = copyTime(stored.LastSync)
		lastSuccessful = copyTime(stored.LastSuccessfulAt)
	}
	s.statusMutex.RUnlock()
	if profileState != nil {
		if lastSync == nil {
			lastSync = copyTime(profileState.LastSync)
		}
		if lastSuccessful == nil {
			lastSuccessful = copyTime(profileState.LastSuccessfulAt)
		}
	}
	return lastSync, lastSuccessful
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
	snapshot.UserID = profileID
	snapshot.RunID = run.runID
	snapshot.RunStartedAt = run.startedAt
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
	lastSync, lastSuccessful := s.priorRunTimes(profileID, nil)
	status := &SyncProfileStatus{
		ProfileID:        profileID,
		ProfileName:      run.profileName,
		DryRun:           run.dryRun,
		LastSync:         lastSync,
		LastAttemptedAt:  timeValue(run.startedAt),
		LastSuccessfulAt: lastSuccessful,
	}
	applySnapshotToStatus(status, snapshot)
	status.Status = statusForSnapshot(&snapshot)
	return status
}

func (s *MultiUserService) persistTerminalSnapshot(profileID string, generation uint64, snapshot sync.SyncSnapshot) error {
	if s.repository == nil {
		return nil
	}
	report, err := runReportFromSnapshot(profileID, generation, snapshot)
	if err != nil {
		return err
	}
	return s.repository.UpsertSyncRunReport(report)
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
	if status.Snapshot != nil && status.Snapshot.State == string(sync.RunPhaseCompleted) && !status.DryRun {
		finishedAt := status.Snapshot.FinishedAt
		if !finishedAt.IsZero() {
			status.LastSync = copyTime(&finishedAt)
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
		snapshot.UserID = profileID
		snapshot.RunID = run.runID
		snapshot.RunStartedAt = run.startedAt
		if snapshot.State == "" || snapshot.State == "idle" {
			snapshot.State = "syncing"
		}
	}
	if snapshot.UserID == "" {
		snapshot.UserID = profileID
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
	// This does not serialize unrelated profiles and lets us reject an older
	// successful worker before it can advance durable success metadata.
	gate := s.profileGate(profileID)
	gate.mu.Lock()
	defer gate.mu.Unlock()
	if status.Snapshot.State == string(sync.RunPhaseCompleted) && !s.latestRunIsCurrent(profileID, runID, generation) {
		return false
	}
	if s.latestRunWasCanceled(profileID, runID, generation) {
		return false
	}
	if status.Snapshot.State == string(sync.RunPhaseFailed) && status.Snapshot.RunError == "" {
		status.Snapshot.RunError = status.Error
	}

	// Durable report persistence is deliberately outside syncMutex. A blocked
	// database operation for one profile must not stall status or start/cancel
	// operations for another profile.
	if err := s.persistTerminalSnapshot(profileID, generation, *status.Snapshot); err != nil {
		status = cloneProfileStatus(status)
		status.Status = "error"
		status.Error = fmt.Sprintf("failed to persist sync report: %v", err)
		status.Snapshot.State = string(sync.RunPhaseFailed)
		status.Snapshot.RunError = status.Error
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

func cloneBookMismatch(record mismatch.BookMismatch) mismatch.BookMismatch {
	copyOf := record
	copyOf.AuthorIDs = append([]int(nil), record.AuthorIDs...)
	copyOf.NarratorIDs = append([]int(nil), record.NarratorIDs...)
	return copyOf
}

func cloneSyncSummary(summary *sync.SyncSummary) *sync.SyncSummary {
	if summary == nil {
		return nil
	}
	copyOf := &sync.SyncSummary{
		UserID:              summary.UserID,
		TotalBooksProcessed: summary.TotalBooksProcessed,
		BooksSynced:         summary.BooksSynced,
		BooksTotal:          summary.BooksTotal,
		BooksNotFound:       append([]sync.BookNotFoundInfo(nil), summary.BooksNotFound...),
		Mismatches:          make([]mismatch.BookMismatch, len(summary.Mismatches)),
	}
	for i, record := range summary.Mismatches {
		copyOf.Mismatches[i] = cloneBookMismatch(record)
	}
	return copyOf
}

func cloneSyncSnapshot(snapshot sync.SyncSnapshot) *sync.SyncSnapshot {
	copyOf := snapshot
	copyOf.BookOutcomes = append([]sync.BookOutcomeRecord(nil), snapshot.BookOutcomes...)
	copyOf.AttentionRecords = append([]sync.BookOutcomeRecord(nil), snapshot.AttentionRecords...)
	copyOf.BooksNotFound = append([]sync.BookNotFoundInfo(nil), snapshot.BooksNotFound...)
	copyOf.Mismatches = make([]mismatch.BookMismatch, len(snapshot.Mismatches))
	for i, record := range snapshot.Mismatches {
		copyOf.Mismatches[i] = cloneBookMismatch(record)
	}
	return &copyOf
}

func applySnapshotToStatus(status *SyncProfileStatus, snapshot sync.SyncSnapshot) {
	if status == nil {
		return
	}
	status.Snapshot = cloneSyncSnapshot(snapshot)
	status.BooksTotal = int(snapshot.BooksTotal)
	status.BooksSynced = int(snapshot.BooksSynced)
	status.BooksNotFound = append([]sync.BookNotFoundInfo(nil), snapshot.BooksNotFound...)
	status.Mismatches = make([]mismatch.BookMismatch, len(snapshot.Mismatches))
	for i, record := range snapshot.Mismatches {
		status.Mismatches[i] = cloneBookMismatch(record)
	}
	status.LastSyncSummary = &sync.SyncSummary{
		UserID:              snapshot.UserID,
		TotalBooksProcessed: snapshot.TotalBooksProcessed,
		BooksSynced:         snapshot.BooksSynced,
		BooksTotal:          snapshot.BooksTotal,
		BooksNotFound:       []sync.BookNotFoundInfo{},
		Mismatches:          []mismatch.BookMismatch{},
	}
}

func cloneProfileStatus(status *SyncProfileStatus) *SyncProfileStatus {
	if status == nil {
		return nil
	}
	copyOf := *status
	if status.LastSync != nil {
		lastSync := *status.LastSync
		copyOf.LastSync = &lastSync
	}
	if status.LastAttemptedAt != nil {
		lastAttempted := *status.LastAttemptedAt
		copyOf.LastAttemptedAt = &lastAttempted
	}
	if status.LastSuccessfulAt != nil {
		lastSuccessful := *status.LastSuccessfulAt
		copyOf.LastSuccessfulAt = &lastSuccessful
	}
	copyOf.BooksNotFound = append([]sync.BookNotFoundInfo(nil), status.BooksNotFound...)
	copyOf.Mismatches = make([]mismatch.BookMismatch, len(status.Mismatches))
	for i, record := range status.Mismatches {
		copyOf.Mismatches[i] = cloneBookMismatch(record)
	}
	copyOf.LastSyncSummary = cloneSyncSummary(status.LastSyncSummary)
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

// IsProfileSyncing checks if a profile is currently syncing
func (s *MultiUserService) IsProfileSyncing(profileID string) bool {
	s.syncMutex.RLock()
	defer s.syncMutex.RUnlock()

	_, exists := s.activeSyncs[profileID]
	return exists
}
