// Sync Profile Management App
const STATUS_LOAD_TIMEOUT_MS = 15000;
const PROFILE_RETRY_BASE_MS = 5000;
const PROFILE_RETRY_MAX_MS = 60000;
// Global image error handler for cover fallbacks
window.__absHandleImageError = function(img) {
    try {
        const raw = (img.dataset && img.dataset.fallbacks) ? img.dataset.fallbacks : '';
        const list = raw.split('|').filter(Boolean);
        let idx = parseInt(img.dataset.fbIdx || '0', 10);
        if (Number.isNaN(idx)) idx = 0;
        if (idx < list.length - 1) {
            idx += 1;
            img.dataset.fbIdx = String(idx);
            img.src = list[idx];
        } else {
            // Stop further error loops
            img.onerror = null;
            // Final fallback (in case list didn't include it)
            img.src = '/cover-placeholder.svg';
        }
    } catch (e) {
        console.warn('Image fallback handler error:', e);
        img.onerror = null;
        img.src = '/cover-placeholder.svg';
    }
};

class SyncProfileApp {
    constructor() {
        this.users = [];
        this.statuses = Object.create(null);
        this.actionErrors = new Map();
        this.currentEditUser = null;
        this.refreshInterval = null;
        this.currentUser = null;
        this.authEnabled = false;
        this.hasRedirectedToLogin = false;
        this.autoRefreshEnabled = true; // Auto-refresh is enabled by default
        this.statusLoadSequence = 0;
        this.activeStatusLoads = 0;
        this.activeStatusRequests = 0;
        this.statusLoadController = null;
        this.profileLoadFailed = false;
        this.profileRetryFailures = 0;
        this.nextProfileRetryAt = 0;
        this.statusRefreshError = null;
        this.openSummary = null;
        this.statusRefreshQueued = false;
        this.statusRefreshWaiters = [];
        // The public aggregate intentionally omits raw run errors. A terminal
        // card may hydrate its error from the authenticated status route with
        // bounded retries for transient failures.
        this.terminalErrorCache = new Map();
        this.terminalErrorRetries = new Map();
        this.terminalErrorRequests = new Map();
        this.authSessionGeneration = 0;
        this.editProfileRequest = null;
        this.sessionMutationRequests = new Map();

        this.init();
    }

    // Coerce various representations to boolean with a sensible default
    toBool(value, defaultValue = true) {
        if (value === true || value === false) return value;
        if (typeof value === 'string') {
            const v = value.trim().toLowerCase();
            if (v === 'true') return true;
            if (v === 'false') return false;
            if (v === '1') return true;
            if (v === '0') return false;
        }
        if (value === 1) return true;
        if (value === 0) return false;
        return !!defaultValue;
    }

    profileUrl(profileId, suffix = '') {
        return `/api/profiles/${encodeURIComponent(String(profileId))}${suffix}`;
    }

    isViewer() {
        return Boolean(this.authEnabled && this.currentUser
            && String(this.currentUser.role || '').toLowerCase() === 'viewer');
    }

    beginSessionMutation(key) {
        this.sessionMutationRequests.get(key)?.controller?.abort();
        const request = {
            generation: this.authSessionGeneration,
            controller: typeof AbortController === 'undefined' ? null : new AbortController()
        };
        this.sessionMutationRequests.set(key, request);
        return request;
    }

    isCurrentSessionMutation(key, request) {
        return this.authSessionGeneration === request.generation
            && this.sessionMutationRequests.get(key) === request
            && !request.controller?.signal.aborted;
    }

    finishSessionMutation(key, request) {
        if (this.sessionMutationRequests.get(key) !== request) return false;
        this.sessionMutationRequests.delete(key);
        return true;
    }

    abortSessionMutations() {
        this.sessionMutationRequests.forEach(request => request.controller?.abort());
        this.sessionMutationRequests.clear();
    }

    updateViewerControls() {
        const viewer = this.isViewer();
        const addProfileTab = [...document.querySelectorAll('.tab-button')]
            .find(button => button.getAttribute('onclick') === "showTab('add-user')");
        const addProfileContent = document.getElementById('add-user-tab');

        if (viewer && addProfileContent?.classList.contains('active')) {
            this.showTab('users');
        }
        if (addProfileTab) {
            addProfileTab.hidden = viewer;
            addProfileTab.setAttribute('aria-hidden', String(viewer));
            addProfileTab.style.display = viewer ? 'none' : '';
        }
        if (addProfileContent) {
            addProfileContent.hidden = viewer;
            addProfileContent.setAttribute('aria-hidden', String(viewer));
            addProfileContent.style.display = viewer ? 'none' : '';
        }
        if (viewer && document.getElementById('edit-user-modal')?.style.display === 'block') {
            this.closeEditModal();
        }

        // Re-render already-loaded cards when the session role changes so a
        // viewer never retains controls from a previous authenticated session.
        if (this.users.length > 0) this.renderProfiles();
        if (Object.keys(this.statuses).length > 0) this.renderStatuses();
    }

    // Format a timestamp to relative time (e.g., "5 minutes ago") with fallback
    formatRelativeTime(ts) {
        try {
            if (!ts) return '';
            const date = (ts instanceof Date) ? ts : new Date(ts);
            const now = new Date();
            const diffMs = date.getTime() - now.getTime();
            const seconds = Math.round(diffMs / 1000);
            const absSec = Math.abs(seconds);
            const rtf = new Intl.RelativeTimeFormat(undefined, { numeric: 'auto' });
            const divisions = [
                { amount: 60, name: 'seconds' },
                { amount: 60, name: 'minutes' },
                { amount: 24, name: 'hours' },
                { amount: 7, name: 'days' },
                { amount: 4.34524, name: 'weeks' },
                { amount: 12, name: 'months' },
                { amount: Number.POSITIVE_INFINITY, name: 'years' }
            ];
            let duration = seconds;
            for (const division of divisions) {
                if (Math.abs(duration) < division.amount) {
                    return rtf.format(Math.round(duration), division.name);
                }
                duration /= division.amount;
            }
            return date.toLocaleString();
        } catch (_) {
            return new Date(ts).toLocaleString();
        }
    }

    async init() {
        try {
            // Set up event listeners first so UI is responsive
            this.setupEventListeners();
            
            // Check authentication status
            const isAuthenticated = await this.checkAuthStatus();
            
            // If auth is enabled but user is not authenticated, we'll be redirected to login
            if (this.authEnabled && !isAuthenticated) {
                // Don't load any data, just show the login UI
                this.updateUserInfo();
                return;
            }
            
            // If we get here, either auth is disabled or user is authenticated
            try {
                // Status loading owns the initial loading state and loads profiles
                // before fetching their statuses.
                await this.loadStatuses();
                
                // Keep retrying if the initial profile request failed.
                if (this.users.length > 0 || this.profileLoadFailed) {
                    this.startAutoRefresh();
                }
            } catch (error) {
                console.error('Error loading data:', error);
                this.showToast('Failed to load data', 'error');
            }
            
            // Ensure UI is up to date
            this.updateUserInfo();
            
        } catch (error) {
            console.error('Error initializing app:', error);
            this.showToast('Failed to initialize application', 'error');
            
            // Make sure we show appropriate UI even if there's an error
            this.updateUserInfo();
        }
    }

    async checkAuthStatus() {
        try {
            // Prevent login loops by checking if we're already on login page
            if (window.location.pathname.endsWith('/login')) {
                // We're on login page, don't do auth checks that might redirect
                this.authEnabled = true;
                this.currentUser = null;
                this.updateUserInfo();
                return false;
            }
            
            // Load current user to determine auth status
            const userLoaded = await this.loadCurrentUser();
            
            if (userLoaded) {
                // User is authenticated
                this.updateUserInfo();
                return true;
            }
            
            // No user loaded - check if we need to redirect to login
            if (this.authEnabled && !this.hasRedirectedToLogin) {
                this.hasRedirectedToLogin = true;
                this.redirectToLogin();
                return false;
            }
            
            // Update UI and return status
            this.updateUserInfo();
            return false;
        } catch (error) {
            console.error('Error checking auth status:', error);
            this.authEnabled = false;
            this.currentUser = null;
            this.updateUserInfo();
            return false;
        }
    }

    async loadCurrentUser() {
        try {
            const response = await fetch('/api/auth/me', {
                method: 'GET',
                credentials: 'include',
                headers: {
                    'Accept': 'application/json',
                    'Cache-Control': 'no-cache',
                    'Pragma': 'no-cache'
                }
            });
            
            if (response.ok) {
                const data = await response.json();
                
                // Handle new authentication response format
                this.authEnabled = data.auth_enabled !== false; // Default to true if not specified
                
                if (data.authenticated && data.user) {
                    this.currentUser = data.user;
                    return true;
                } else {
                    // Not authenticated but auth is enabled
                    this.currentUser = null;
                    return false;
                }
            } else {
                // If we get an error, assume auth is enabled but user not authenticated
                this.currentUser = null;
                this.authEnabled = true;
                return false;
            }
        } catch (error) {
            console.error('Error loading current user:', error);
            // On error, assume auth is disabled
            this.currentUser = null;
            this.authEnabled = false;
            return false;
        }
    }

    redirectToLogin() {
        // Only redirect if we're not already on the login page
        if (!window.location.pathname.endsWith('/login')) {
            const currentPath = window.location.pathname + window.location.search;
            window.location.href = `/login?redirect=${encodeURIComponent(currentPath)}`;
        }
    }

    updateUserInfo() {
        try {
            const userInfoElement = document.getElementById('user-info');
            if (!userInfoElement) {
                console.warn('User info element not found');
                return;
            }

            if (this.authEnabled && this.currentUser) {
                // User is authenticated - show user info and logout button
                // Keycloak might provide different user properties, so we'll check multiple possibilities
                const username = this.currentUser.preferred_username || 
                               this.currentUser.name || 
                               this.currentUser.email || 
                               this.currentUser.username || 
                               'User';
                const userInitial = username.charAt(0).toUpperCase();
                
                userInfoElement.innerHTML = `
                    <div class="user-info">
                        <div class="user-avatar">${userInitial}</div>
                        <span>${this.escapeHtml(username)}</span>
                    </div>
                    <button class="logout-btn" onclick="app.logout()">
                        <span class="btn-icon">🚪</span> Logout
                    </button>
                `;
                
                // Make sure the user is on the right page
                if (window.location.pathname.endsWith('/login')) {
                    window.location.href = '/';
                }
            } else if (this.authEnabled) {
                // Auth is enabled but no user - show login button
                userInfoElement.innerHTML = `
                    <button class="login-btn" onclick="app.redirectToLogin()">
                        <span class="btn-icon">🔑</span> Login
                    </button>
                `;
                
                // If we're not on the login page and auth is required, redirect
                if (!window.location.pathname.endsWith('/login')) {
                    this.redirectToLogin();
                }
            } else {
                // Auth is not enabled - clear the user info area
                userInfoElement.innerHTML = '';
            }
            
            // Trigger a reflow to ensure UI updates
            userInfoElement.offsetHeight;
            this.updateViewerControls();
            
        } catch (error) {
            console.error('Error updating user info:', error);
        }
    }

    async logout() {
        try {
            // Clear local state first to update UI immediately
            this.editProfileRequest?.controller?.abort();
            this.editProfileRequest = null;
            this.currentUser = null;
            this.authEnabled = true;
            this.resetSessionBoundState();
            this.updateUserInfo();
            
            // Show loading state
            this.showLoading();
            
            // Call the logout API
            const response = await fetch('/api/auth/logout', {
                method: 'POST',
                credentials: 'include',
                headers: {
                    'Cache-Control': 'no-cache',
                    'Pragma': 'no-cache'
                }
            });
            
            // Hide loading state
            this.hideLoading();
            
            // Handle response
            if (response.ok) {
                // Stop any auto-refresh
                this.stopAutoRefresh();
                
                // Redirect to login page
                window.location.href = '/login';
            } else {
                const errorData = await response.json().catch(() => ({}));
                console.error('Logout failed:', response.status, errorData);
                this.showToast('Logout failed. Please try again.', 'error');
                
                // Still redirect to login page even if API call fails
                window.location.href = '/login';
            }
        } catch (error) {
            console.error('Logout error:', error);
            this.hideLoading();
            this.showToast('Logout failed. Please try again.', 'error');
            
            // Still redirect to login page on error
            window.location.href = '/login';
        }
    }

    setupEventListeners() {
        const bindProfileActions = (containerId, cardSelector) => {
            const container = document.getElementById(containerId);
            container.addEventListener('click', (event) => {
                const button = event.target.closest('button[data-profile-action]');
                if (!button || !container.contains(button)) return;

                const card = button.closest(cardSelector);
                if (!card || !container.contains(card)) return;
                const profileId = card.dataset.profileId;

                if (this.isViewer() && ['edit', 'delete', 'start', 'cancel'].includes(button.dataset.profileAction)) {
                    return;
                }

                switch (button.dataset.profileAction) {
                    case 'edit': this.editProfile(profileId); break;
                    case 'delete': this.deleteProfile(profileId); break;
                    case 'start': this.startSync(profileId); break;
                    case 'cancel': this.cancelSync(profileId); break;
                    case 'summary': this.toggleSyncSummary(profileId); break;
                    case 'dismiss-error':
                        this.actionErrors.delete(profileId);
                        this.renderStatuses();
                        break;
                }
            });
        };
        bindProfileActions('users-list', '.user-card[data-profile-id]');
        bindProfileActions('sync-status', '.status-card[data-profile-id]');

        document.getElementById('sync-summary-content').addEventListener('click', (event) => {
            if (!event.target.closest('[data-details-retry]')) return;
            const open = this.openSummary;
            if (open) this.fetchAndRenderDetails({ open, preservePosition: true });
        });

        // Tab switching
        document.querySelectorAll('.tab-button').forEach(button => {
            button.addEventListener('click', (e) => {
                const tabName = e.target.getAttribute('onclick').match(/'([^']+)'/)[1];
                this.showTab(tabName);
            });
        });

        // Add profile form
        document.getElementById('add-user-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.handleAddProfile(e);
        });

        // Edit profile form
        document.getElementById('edit-user-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.handleEditProfile(e);
        });

        // Modal close on background click
        document.getElementById('edit-user-modal').addEventListener('click', (e) => {
            if (e.target.id === 'edit-user-modal') {
                this.closeEditModal();
            }
        });
    }

    showTab(tabName) {
        if (this.isViewer() && tabName === 'add-user') {
            tabName = 'users';
        }
        // Update tab buttons
        document.querySelectorAll('.tab-button').forEach(btn => btn.classList.remove('active'));
        document.querySelector(`[onclick="showTab('${tabName}')"]`).classList.add('active');

        // Update tab content
        document.querySelectorAll('.tab-content').forEach(content => content.classList.remove('active'));
        document.getElementById(`${tabName}-tab`).classList.add('active');

        // Refresh data when switching to relevant tabs
        if (tabName === 'users') {
            this.loadProfiles();
        } else if (tabName === 'sync') {
            this.loadStatuses();
        }
    }

    /**
     * Renders the list of sync profiles in the UI with improved visual design
     */
    renderProfiles() {
        const usersList = document.getElementById('users-list');
        if (!usersList) return;

        if (!this.users || this.users.length === 0) {
            usersList.innerHTML = `
                <div class="empty-state" style="grid-column: 1 / -1; text-align: center; padding: 2rem;">
                    <h3>No sync profiles found</h3>
                    <p>${this.isViewer() ? 'No profiles are available.' : 'Click on "Add Profile" to create a new sync profile.'}</p>
                </div>
            `;
            return;
        }

        usersList.innerHTML = this.users.map(user => {
            const lastSyncISO = user.last_sync || null;
            const lastSync = lastSyncISO ? this.formatRelativeTime(lastSyncISO) : 'Never';
            const statusClass = user.active ? 'active' : 'inactive';
            const statusIcon = user.active ? '✓' : '✗';
            
            return `
                <div class="user-card" data-profile-id="${this.escapeHtmlAttribute(user.id)}">
                    <div class="user-card-header">
                        <h3>${this.escapeHtml(user.name || user.id)}</h3>
                        <span class="status-badge ${statusClass}" title="${user.active ? 'Active' : 'Inactive'}">
                            ${statusIcon} ${user.active ? 'Active' : 'Inactive'}
                        </span>
                    </div>
                    
                    <div class="user-card-body">
                        <div class="user-info">
                            <div class="user-info-item">
                                <strong>Profile ID:</strong>
                                <span class="user-id">${this.escapeHtml(user.id)}</span>
                            </div>
                            <div class="user-info-item">
                                <strong>Last Synced:</strong>
                                <span class="last-sync" title="${lastSyncISO ? new Date(lastSyncISO).toLocaleString() : 'Never'}">${lastSync}</span>
                            </div>
                        </div>
                        
                        ${this.isViewer() ? '' : `<div class="user-card-actions">
                            <button class="btn btn-sm btn-icon" data-profile-action="edit" title="Edit Profile">
                                <span class="icon">✏️</span> Edit
                            </button>
                            <button class="btn btn-sm btn-icon btn-danger" data-profile-action="delete" title="Delete Profile">
                                <span class="icon">🗑️</span> Delete
                            </button>
                            <button class="btn btn-sm btn-primary" data-profile-action="start" ${user.active ? '' : 'disabled'}>
                                <span class="icon">🔄</span> Sync Now
                            </button>
                        </div>`}
                    </div>
                </div>
            `;
        }).join('');
    }

    async fetchJsonWithTimeout(url, options = {}) {
        const { signal: requestSignal, ...fetchOptions } = options;
        if (typeof AbortController === 'undefined') {
            const response = await fetch(url, fetchOptions);
            return { response, data: await response.json() };
        }

        const controller = new AbortController();
        let timedOut = false;
        const timeout = setTimeout(() => {
            timedOut = true;
            controller.abort();
        }, STATUS_LOAD_TIMEOUT_MS);
        const abortForRequest = () => controller.abort(requestSignal.reason);

        if (requestSignal) {
            if (requestSignal.aborted) {
                abortForRequest();
            } else {
                requestSignal.addEventListener('abort', abortForRequest, { once: true });
            }
        }

        try {
            const response = await fetch(url, { ...fetchOptions, signal: controller.signal });
            const data = await response.json().catch(() => ({}));
            if (controller.signal.aborted) {
                const error = new Error(timedOut ? 'Request timed out' : 'Request aborted');
                error.name = timedOut ? 'TimeoutError' : 'AbortError';
                throw error;
            }
            return { response, data };
        } catch (error) {
            // fetch() rejects with AbortError before reaching the response path.
            // Keep a real timeout distinct from an intentional parent cancellation.
            if (timedOut && error.name === 'AbortError') {
                const timeoutError = new Error('Request timed out');
                timeoutError.name = 'TimeoutError';
                throw timeoutError;
            }
            throw error;
        } finally {
            clearTimeout(timeout);
            requestSignal?.removeEventListener('abort', abortForRequest);
        }
    }

    async loadProfiles({ showLoading = true, statusOwned = false, signal } = {}) {
        const authGeneration = this.authSessionGeneration;
        try {
            if (showLoading) this.showLoading();
            
            // Check authentication status first
            if (this.authEnabled && !this.currentUser) {
                this.resetProfileRetry();
                this.showToast('Please log in to view profiles', 'error');
                this.redirectToLogin();
                return;
            }
            
            const { response, data } = await this.fetchJsonWithTimeout('/api/profiles', {
                method: 'GET',
                credentials: 'include', // Include session cookies
                headers: {
                    'Content-Type': 'application/json'
                },
                signal
            });

            // A session switch may have happened while the request was in
            // flight. Do not let the old response redirect or otherwise
            // mutate the UI for the new session.
            if (authGeneration !== this.authSessionGeneration) return;
            
            // Handle authentication errors specifically
            if (response.status === 401 || response.status === 403) {
                this.resetProfileRetry();
                this.showToast('Authentication required. Please log in.', 'error');
                this.redirectToLogin();
                return;
            }
            
            if (response.ok && data.success) {
                this.resetProfileRetry();
                this.users = data.data;
                this.renderProfiles();
            } else {
                // Handle different types of errors
                if (data.error && data.error.code === 'authentication_required') {
                    this.resetProfileRetry();
                    this.showToast('Authentication required. Please log in.', 'error');
                    this.redirectToLogin();
                } else {
                    const message = 'Failed to load sync profiles: ' + (data.error?.message || data.error || 'Unknown error');
                    if (statusOwned) throw new Error(message);
                    this.showToast(message, 'error');
                }
            }
        } catch (error) {
            if (authGeneration !== this.authSessionGeneration) return;
            if (statusOwned) throw error;
            this.showToast('Error loading sync profiles: ' + error.message, 'error');
        } finally {
            if (showLoading && authGeneration === this.authSessionGeneration) this.hideLoading();
        }
    }

    resetProfileRetry() {
        this.profileLoadFailed = false;
        this.profileRetryFailures = 0;
        this.nextProfileRetryAt = 0;
    }

    async loadStatuses({ silent = false } = {}) {
        // Timer-driven refreshes must never overlap. Keeping the existing
        // request alive also lets its stale-response guard remain effective.
        if (this.activeStatusRequests > 0) {
            if (silent) return;
            this.statusRefreshQueued = true;
            return new Promise(resolve => this.statusRefreshWaiters.push(resolve));
        }
        const requestSequence = ++this.statusLoadSequence;
        const controller = typeof AbortController === 'undefined' ? null : new AbortController();
        this.statusLoadController = controller;
        const signal = controller?.signal;
        const isCurrentRequest = () => requestSequence === this.statusLoadSequence && !signal?.aborted;
        this.activeStatusRequests += 1;
        try {
            if (!silent) {
                this.activeStatusLoads += 1;
                this.showLoading();
            }
            // First, get the list of profiles if not already loaded
            if (!this.users || this.users.length === 0) {
                // This status request owns its loading feedback. Avoid letting
                // the nested profile request hide it before statuses finish.
                await this.loadProfiles({ showLoading: false, statusOwned: true, signal });
            }
            
            // If no users, render empty status
            if (!this.users || this.users.length === 0) {
                if (!isCurrentRequest()) return;
                this.statuses = Object.create(null);
                this.pruneTerminalErrorState();
                this.renderStatuses();
                return;
            }
            if (this.authEnabled && !(await this.validateSession(signal))) return;
            const { response, data: result } = await this.fetchJsonWithTimeout('/api/status', { signal });
            if (response.status === 401 || response.status === 403) {
                this.handleAuthExpiry();
                return;
            }
            if (!response.ok) throw new Error(`Status request failed (${response.status})`);
            const statusData = result.success ? result.data : result;
            if (!Array.isArray(statusData)) throw new Error('Status response was not an array');

            if (!isCurrentRequest()) return;
            const authorizedProfileIds = this.authEnabled && this.currentUser
                ? new Set(this.users.map(user => String(user?.id ?? '')))
                : null;
            const statuses = Object.create(null);
            statusData.forEach((status) => {
                if (!status || !status.profile_id) return;
                const profileId = String(status.profile_id);
                if (authorizedProfileIds && !authorizedProfileIds.has(profileId)) return;
                const snapshot = status.snapshot || null;
                const normalized = {
                    profile_id: status.profile_id,
                    profile_name: status.profile_name || `Profile ${profileId}`,
                    status: status.status || 'idle',
                    dry_run: this.toBool(status.dry_run, false),
                    last_sync: status.last_sync || null,
                    progress: status.progress || '',
                    books_total: snapshot?.books_total ?? status.books_total ?? 0,
                    snapshot
                };
                const errorKey = this.terminalErrorIdentity(profileId, normalized);
                if (errorKey && this.terminalErrorCache.has(errorKey)) {
                    normalized.terminal_error = this.terminalErrorCache.get(errorKey);
                }
                statuses[profileId] = normalized;
            });
            this.statuses = statuses;
            this.pruneTerminalErrorState();
            this.statusRefreshError = null;
            this.renderStatuses();
            this.refreshOpenSummary();
            this.fetchTerminalErrorFallbacks(signal, this.authSessionGeneration);

        } catch (error) {
            if (error.name === 'AbortError') return;
            console.error('Error loading sync statuses:', error);
            this.statusRefreshError = error.message;
            // Keep the last good snapshot and its DOM intact during a
            // transient network failure. Only add a passive stale indicator.
            if (this.users.length > 0 && requestSequence === this.statusLoadSequence) {
                this.renderStatuses({ unavailable: true });
                this.renderDetailsStale(this.openSummary);
            }
            if (this.users.length === 0 && requestSequence === this.statusLoadSequence && error.name !== 'AbortError') {
                this.profileLoadFailed = true;
                this.profileRetryFailures = Math.min(this.profileRetryFailures + 1, 5);
                const retryDelay = Math.min(PROFILE_RETRY_BASE_MS * 2 ** (this.profileRetryFailures - 1), PROFILE_RETRY_MAX_MS);
                this.nextProfileRetryAt = Date.now() + retryDelay;
                this.renderStatuses({ unavailable: true });
                this.renderDetailsStale(this.openSummary);
                this.startAutoRefresh();
            }
            if (!silent && requestSequence === this.statusLoadSequence) {
                this.showToast('Error loading statuses: ' + error.message, 'error');
            }
        } finally {
            this.activeStatusRequests -= 1;
            const ownsLoadingUI = requestSequence === this.statusLoadSequence
                && this.statusLoadController === controller
                && !signal?.aborted;
            if (this.statusLoadController === controller) {
                this.statusLoadController = null;
            }
            if (!silent) {
                this.activeStatusLoads -= 1;
                if (this.activeStatusLoads === 0 && ownsLoadingUI) this.hideLoading();
            }
            if (this.activeStatusRequests === 0 && this.statusRefreshQueued) {
                this.statusRefreshQueued = false;
                const waiters = this.statusRefreshWaiters.splice(0);
                this.loadStatuses({ silent: true }).then(() => {
                    waiters.forEach(resolve => resolve());
                });
            }
        }
    }

    terminalErrorIdentity(profileId, status) {
        const snapshot = status?.snapshot || {};
        const state = String(snapshot.state || status?.status || '').toLowerCase();
        if (state !== 'failed' && state !== 'error') return null;
        const runId = snapshot.run_id;
        if (runId) return JSON.stringify([String(profileId), String(runId)]);
        // Older terminal snapshots may not carry a run ID. Include the most
        // stable available run marker, falling back only when none exists.
        const marker = snapshot.run_started_at || status?.last_sync;
        return JSON.stringify([String(profileId), 'terminal', state, marker || '']);
    }

    pruneTerminalErrorState() {
        const liveProfileIds = new Set(this.users.map(user => String(user?.id || '')));
        const currentFailures = new Set();
        Object.entries(this.statuses).forEach(([profileId, status]) => {
            const identity = this.terminalErrorIdentity(profileId, status);
            if (identity) currentFailures.add(identity);
        });
        for (const identity of this.terminalErrorCache.keys()) {
            if (!currentFailures.has(identity)) this.terminalErrorCache.delete(identity);
        }
        for (const identity of this.terminalErrorRetries.keys()) {
            if (!currentFailures.has(identity)) this.terminalErrorRetries.delete(identity);
        }
        for (const profileId of this.actionErrors.keys()) {
            if (!liveProfileIds.has(String(profileId))) this.actionErrors.delete(profileId);
        }
    }

    fetchTerminalErrorFallbacks(signal, authGeneration) {
        Object.entries(this.statuses).forEach(([profileId, status]) => {
            const identity = this.terminalErrorIdentity(profileId, status);
            if (!identity || this.terminalErrorCache.has(identity) || this.terminalErrorRequests.has(identity)) return;
            const retry = this.terminalErrorRetries.get(identity);
            if (retry && retry.retryAt > Date.now()) return;
            const request = this.fetchTerminalError(profileId, identity, signal, authGeneration);
            this.terminalErrorRequests.set(identity, request);
            request.finally(() => {
                if (this.terminalErrorRequests.get(identity) === request) {
                    this.terminalErrorRequests.delete(identity);
                }
            });
        });
    }

    scheduleTerminalErrorRetry(profileId, identity, authGeneration) {
        if (authGeneration !== this.authSessionGeneration
            || this.terminalErrorIdentity(profileId, this.statuses[profileId]) !== identity) return;
        const previous = this.terminalErrorRetries.get(identity);
        const failures = Math.min((previous?.failures || 0) + 1, 6);
        const delay = Math.min(15000 * 2 ** (failures - 1), 300000);
        this.terminalErrorRetries.set(identity, { failures, retryAt: Date.now() + delay });
    }

    async fetchTerminalError(profileId, identity, signal, authGeneration) {
        try {
            const { response, data: result } = await this.fetchJsonWithTimeout(
                this.profileUrl(profileId, '/status'),
                { signal }
            );
            if (response.status === 401 || response.status === 403) {
                this.handleAuthExpiry();
                return;
            }
            if (response.status !== 200) throw new Error(`Terminal status request failed (${response.status})`);
            if (!result || result.success !== true || !result.data || typeof result.data !== 'object') {
                throw new Error('Terminal status response was invalid');
            }
            const status = result.data;
            if (String(status.profile_id || '') !== String(profileId)) {
                throw new Error('Terminal status response was for the wrong profile');
            }
            if (this.terminalErrorIdentity(profileId, status) !== identity) {
                throw new Error('Terminal status response was for the wrong failure identity');
            }
            const runId = status.snapshot?.run_id || status.run_id;
            const requestedRunId = JSON.parse(identity)[1];
            if (requestedRunId !== 'terminal' && String(runId || '') !== requestedRunId) {
                throw new Error('Terminal status response was for the wrong run');
            }
            if (Object.prototype.hasOwnProperty.call(status, 'error') && typeof status.error !== 'string') {
                throw new Error('Terminal status response contained an invalid error');
            }
            if (authGeneration !== this.authSessionGeneration
                || this.terminalErrorIdentity(profileId, this.statuses[profileId]) !== identity) return;
            const error = typeof status?.error === 'string' ? status.error : '';
            this.terminalErrorCache.set(identity, error);
            this.terminalErrorRetries.delete(identity);
            const current = this.statuses[profileId];
            current.terminal_error = error;
            this.renderStatuses();
            this.renderOpenSummaryError(profileId, identity, error);
        } catch (error) {
            if (error.name !== 'AbortError') {
                console.error('Error loading terminal sync status:', error);
            }
            this.scheduleTerminalErrorRetry(profileId, identity, authGeneration);
        }
    }

    renderOpenSummaryError(profileId, identity, error) {
        const open = this.openSummary;
        if (!open || open.profileId !== profileId) return;
        const identityParts = JSON.parse(identity);
        const runId = identityParts[1] === 'terminal' ? '' : identityParts[1];
        if (!runId || open.runId !== runId) return;
        const content = document.getElementById('sync-summary-content');
        const summary = content?.querySelector('.sync-summary');
        if (!content || !summary || summary.dataset.runId !== runId) return;
        const viewport = this.captureDetailViewport(content);
        let errorNode = summary.querySelector('[data-run-error]');
        if (!error) {
            errorNode?.remove();
            this.restoreDetailViewport(content, viewport);
            return;
        }
        if (!errorNode) {
            errorNode = document.createElement('div');
            errorNode.dataset.runError = 'true';
            errorNode.className = 'status-message status-error';
            summary.prepend(errorNode);
        }
        errorNode.innerHTML = `<strong>Run error:</strong> ${this.escapeHtml(error)}`;
        this.restoreDetailViewport(content, viewport);
    }

    async validateSession(signal) {
        const previousUserId = this.currentUser?.id == null ? null : String(this.currentUser.id);
        const previousRole = String(this.currentUser?.role || '').trim().toLowerCase();
        try {
            const { response, data } = await this.fetchJsonWithTimeout('/api/auth/me', {
                credentials: 'include',
                headers: {
                    'Accept': 'application/json',
                    'Cache-Control': 'no-cache',
                    'Pragma': 'no-cache'
                },
                signal
            });
            const authEnabled = data.auth_enabled !== false;
            if (response.status === 401 || response.status === 403 || (authEnabled && data.authenticated === false)) {
                this.handleAuthExpiry();
                return false;
            }
            if (response.ok && !authEnabled) {
                this.authEnabled = false;
                this.currentUser = null;
            } else if (response.ok && data.authenticated && data.user) {
                this.currentUser = data.user;
            }
            const currentUserId = this.currentUser?.id == null ? null : String(this.currentUser.id);
            const currentRole = String(this.currentUser?.role || '').trim().toLowerCase();
            const sessionBoundaryChanged = previousUserId !== currentUserId || previousRole !== currentRole;
            if (sessionBoundaryChanged) {
                this.resetSessionBoundState();
                // This validation runs inside loadStatuses. Queue one fresh
                // request after the stale request unwinds so profile access
                // and status cards are rebuilt for the new authorization
                // boundary.
                this.statusRefreshQueued = true;
            }
            if (sessionBoundaryChanged) {
                this.updateUserInfo();
            }
            return true;
        } catch (error) {
            if (error.name === 'AbortError') return false;
            // A transient validation failure should not log the user out or
            // replace the last-good aggregate status snapshot.
            return true;
        }
    }

    resetSessionBoundState() {
        this.abortSessionMutations();
        this.editProfileRequest?.controller?.abort();
        this.editProfileRequest = null;
        this.users = [];
        this.statuses = Object.create(null);
        this.closeEditModal();
        this.statusRefreshError = null;
        this.actionErrors.clear();
        this.terminalErrorCache.clear();
        this.terminalErrorRetries.clear();
        this.terminalErrorRequests.clear();
        this.resetProfileRetry();
        this.authSessionGeneration += 1;
        this.statusLoadSequence += 1;
        this.statusLoadController?.abort();
        this.statusRefreshQueued = false;
        this.statusRefreshWaiters.splice(0).forEach(resolve => resolve());
        this.clearOpenSummary();
        this.renderProfiles();
        this.renderStatuses();
    }
    
    renderStatusCard(profileId, status) {
        const snapshot = status.snapshot || {};
        const counts = snapshot.outcome_counts || {};
        const processed = Number(snapshot.processed_so_far || 0);
        const booksTotal = Number(snapshot.books_total ?? status.books_total ?? 0);
        const progressPercent = booksTotal > 0 ? Math.min(100, Math.round((processed / booksTotal) * 100)) : 0;
        const hasKnownTotal = booksTotal > 0;
        const hasProcessedBooks = processed > 0;
        const lastSync = status.last_sync || null;
        const statusState = (snapshot.state || status.status || 'idle').toLowerCase();
        const statusClass = statusState === 'failed' ? 'error' : statusState;
        const statusText = statusState === 'syncing' && status.dry_run ? 'Syncing (dry run)' : this.formatStatusLabel(statusState);
        const profileName = status.profile_name || status.profile_id || 'Unknown Profile';
        const actionError = this.actionErrors.get(profileId);
        const hasRun = Boolean(snapshot.run_id);
        const detailsOpen = this.isSyncSummaryOpen(profileId, snapshot.run_id);
        const retryable = statusState === 'error' || statusState === 'failed';
        const categories = this.outcomeCategories(counts);

        return `
                <div class="status-card ${statusClass}" data-profile-id="${this.escapeHtmlAttribute(profileId)}">
                    <div class="status-header">
                        <h3>${this.escapeHtml(profileName)}</h3>
                        <span class="status-badge">${this.escapeHtml(statusText)}</span>
                    </div>
                    <div class="status-info">
                        ${lastSync ? `
                            <div><strong>Last Sync:</strong> <span class="relative-sync-time" title="${new Date(lastSync).toLocaleString()}">${this.formatRelativeTime(lastSync)}</span></div>
                        ` : ''}
                        ${hasKnownTotal ? `
                            <div><strong>Processed:</strong> ${processed} of ${booksTotal}</div>
                            <div class="progress-bar">
                                <div class="progress-fill" style="width: ${progressPercent}%"></div>
                            </div>
                        ` : hasProcessedBooks ? `
                            <div><strong>Processed:</strong> ${processed} (total unknown)</div>
                        ` : ''}
                        ${hasRun ? `
                            <div class="sync-summary-stats outcome-counts" aria-label="Sync outcome counts">
                                ${categories.map(category => `<span class="stat ${category.tone}">${category.label}: ${category.count}</span>`).join('')}
                            </div>
                        ` : ''}
                        ${status.message ? `
                            <div class="status-message">${this.escapeHtml(status.message)}</div>
                        ` : ''}
                        ${status.terminal_error ? `
                            <div class="status-message status-error"><strong>Run error:</strong> ${this.escapeHtml(status.terminal_error)}</div>
                        ` : ''}
                        ${status.unavailable || this.statusRefreshError ? `
                            <div class="status-message" role="status">Status unavailable. Showing last known data.</div>
                        ` : ''}
                        ${actionError ? `
                            <div class="action-error" role="alert">
                                <span>${this.escapeHtml(actionError.action)} failed: ${this.escapeHtml(actionError.message)}</span>
                                <button class="btn btn-sm" data-profile-action="dismiss-error" aria-label="Dismiss action error">Dismiss</button>
                            </div>
                        ` : ''}
                    </div>
                    <div class="status-actions">
                        ${this.isViewer() ? '' : (statusState.toLowerCase() === 'syncing' ? `
                            <button class="btn btn-warning" data-profile-action="cancel">
                                Cancel Sync
                            </button>
                        ` : `
                            <button class="btn btn-primary" data-profile-action="start">
                                ${retryable ? 'Retry Sync' : 'Start Sync'}
                            </button>
                        `)}
                        ${hasRun ? `
                            <button class="btn btn-secondary" data-profile-action="summary" aria-expanded="${detailsOpen}">
                                ${detailsOpen ? 'Hide Details' : 'View Details'}
                            </button>
                        ` : ''}
                    </div>
                </div>
            `;
    }

    formatStatusLabel(state) {
        return state ? state.charAt(0).toUpperCase() + state.slice(1) : 'Idle';
    }

    outcomeCategories(counts = {}) {
        return [
            { key: 'synced', label: 'Synced', tone: 'success' },
            { key: 'already_current', label: 'Already current', tone: 'info' },
            { key: 'skipped', label: 'Skipped', tone: 'muted' },
            { key: 'needs_review', label: 'Needs review', tone: 'warning' },
            { key: 'not_found', label: 'Not found', tone: 'warning' },
            { key: 'failed', label: 'Failed', tone: 'error' },
            { key: 'would_sync', label: 'Would sync', tone: 'info' }
        ].map(category => ({ ...category, count: Number(counts[category.key] || 0) }));
    }

    updateRelativeSyncTime(card, lastSync) {
        const relativeTime = card.querySelector('.relative-sync-time');
        if (!relativeTime || !lastSync) return;

        relativeTime.textContent = this.formatRelativeTime(lastSync);
        relativeTime.title = new Date(lastSync).toLocaleString();
    }

    renderStatuses({ unavailable = false } = {}) {
        const container = document.getElementById('sync-status');
        if (!container) return;

        if (Object.keys(this.statuses).length === 0) {
            const emptyState = container.querySelector('.status-empty-state');
            if (!emptyState) {
                container.innerHTML = `
                    <div class="text-center status-empty-state" style="grid-column: 1 / -1; padding: 40px;">
                        <h3></h3>
                        <p></p>
                    </div>
                `;
            }
            const state = container.querySelector('.status-empty-state');
            state.querySelector('h3').textContent = unavailable ? 'Unable to load sync statuses' : 'No sync statuses available';
            state.querySelector('p').textContent = unavailable
                ? 'Try refreshing the status in a moment.'
                : 'Add a new sync profile and start syncing to see status information.';
            return;
        }

        const statusArray = Object.entries(this.statuses).filter(([_, status]) => status);
        const existingCards = new Map(
            [...container.querySelectorAll('.status-card[data-profile-id]')]
                .map(card => [card.dataset.profileId, card])
        );
        const retainedProfiles = new Set();
        const activeElement = document.activeElement;
        const scrollPosition = { x: window.scrollX, y: window.scrollY };
        let changed = false;

        // Remove the empty-state message once real status cards are available.
        [...container.children].filter(child => !child.matches('.status-card')).forEach(child => {
            child.remove();
            changed = true;
        });

        statusArray.forEach(([profileId, status], index) => {
            let card = existingCards.get(profileId);
            const snapshotRunId = status.snapshot?.run_id;
            const detailsOpen = this.isSyncSummaryOpen(profileId, snapshotRunId);
            const signature = JSON.stringify([status, this.actionErrors.get(profileId), Boolean(this.statusRefreshError), detailsOpen]);
            const focusInfo = card && activeElement && card.contains(activeElement)
                ? {
                    id: activeElement.id,
                    tagName: activeElement.tagName,
                    action: activeElement.dataset.profileAction,
                    primaryAction: ['start', 'cancel'].includes(activeElement.dataset.profileAction)
                }
                : null;

            if (!card || card.__statusSignature !== signature) {
                const wrapper = document.createElement('div');
                wrapper.innerHTML = this.renderStatusCard(profileId, status).trim();
                const replacement = wrapper.firstElementChild;
                replacement.__statusSignature = signature;
                if (card) {
                    card.replaceWith(replacement);
                } else {
                    container.appendChild(replacement);
                }
                card = replacement;
                changed = true;

                if (focusInfo) {
                    const focusTarget = focusInfo.id
                        ? [...card.querySelectorAll('[id]')].find(element => element.id === focusInfo.id)
                        : [...card.querySelectorAll(focusInfo.tagName)].find(element =>
                            focusInfo.primaryAction
                                ? ['start', 'cancel'].includes(element.dataset.profileAction)
                                : element.dataset.profileAction === focusInfo.action);
                    focusTarget?.focus({ preventScroll: true });
                }
            }

            retainedProfiles.add(profileId);
            this.updateRelativeSyncTime(card, status.last_sync || status.lastSync);
            const cardAtPosition = container.children[index];
            if (cardAtPosition !== card) {
                container.insertBefore(card, cardAtPosition || null);
                changed = true;
            }
        });

        existingCards.forEach((card, profileId) => {
            if (!retainedProfiles.has(profileId)) {
                card.remove();
                changed = true;
            }
        });

        if (changed && typeof window.scrollTo === 'function') {
            window.scrollTo(scrollPosition.x, scrollPosition.y);
        }
    }

    isSyncSummaryOpen(profileId, runId) {
        return Boolean(runId)
            && this.openSummary?.profileId === profileId
            && this.openSummary?.runId === runId;
    }

    async toggleSyncSummary(profileId) {
        const runId = this.statuses[profileId]?.snapshot?.run_id;
        if (this.isSyncSummaryOpen(profileId, runId)) {
            this.clearOpenSummary();
            this.renderStatuses();
            return;
        }
        await this.showSyncSummary(profileId);
    }
    
    async showSyncSummary(profileId) {
        const status = this.statuses[profileId];
        const runId = status?.snapshot?.run_id;
        if (!status || !runId) {
            console.error('No current sync run found for profile:', profileId);
            return;
        }

        const container = document.getElementById('sync-summary-container');
        if (!container) return;
        const previous = this.openSummary;
        const sameRun = previous?.profileId === profileId && previous?.runId === runId;
        previous?.detailsController?.abort();
        this.openSummary = {
            profileId,
            runId,
            generation: (previous?.generation || 0) + 1,
            expandedIds: sameRun ? previous.expandedIds : new Set(),
            expandedOutcomes: sameRun && previous.expandedOutcomes instanceof Set ? previous.expandedOutcomes : new Set(),
            scrollTop: 0
        };
        const open = this.openSummary;
        this.renderStatuses();
        if (!sameRun) this.renderDetailsState('loading', open);
        await this.fetchAndRenderDetails({ open });
        if (!sameRun && this.openSummary === open) container.scrollIntoView({ behavior: 'smooth', block: 'start' });
    }

    async refreshOpenSummary() {
        if (!this.openSummary) return;
        const status = this.statuses[this.openSummary.profileId];
        const runId = status?.snapshot?.run_id;
        if (!runId) {
            this.clearOpenSummary();
            return;
        }
        // Replace the open state object rather than mutating it. An older
        // details response can then never clear or publish the new run.
        if (runId !== this.openSummary.runId) {
            const previous = this.openSummary;
            previous.detailsController?.abort();
            this.openSummary = {
                profileId: previous.profileId,
                runId,
                generation: previous.generation + 1,
                expandedIds: new Set(),
                expandedOutcomes: new Set(),
                scrollTop: 0
            };
            this.renderDetailsState('loading', this.openSummary);
        }
        await this.fetchAndRenderDetails({ open: this.openSummary, preservePosition: true });
    }

    clearOpenSummary() {
        this.openSummary?.detailsController?.abort();
        this.openSummary?.expandedIds?.clear();
        this.openSummary?.expandedOutcomes?.clear();
        this.openSummary = null;
        const container = document.getElementById('sync-summary-container');
        const content = document.getElementById('sync-summary-content');
        const tabs = document.getElementById('sync-summary-tabs');
        if (content) content.replaceChildren();
        if (tabs) tabs.replaceChildren();
        if (container) container.style.display = 'none';
    }

    captureDetailViewport(content) {
        const viewportHeight = window.innerHeight || document.documentElement.clientHeight || 0;
        const visible = (element) => {
            const rect = element.getBoundingClientRect();
            return rect.bottom > 0 && rect.top < viewportHeight;
        };
        const book = [...content.querySelectorAll('[data-book-id]')].find(visible);
        const outcome = book ? null : [...content.querySelectorAll('[data-outcome]')].find(visible);
        const anchor = book
            ? { type: 'book', value: book.dataset.bookId, top: book.getBoundingClientRect().top }
            : outcome
                ? { type: 'outcome', value: outcome.dataset.outcome, top: outcome.getBoundingClientRect().top }
                : null;
        return {
            windowX: window.scrollX || 0,
            windowY: window.scrollY || 0,
            contentTop: content.scrollTop || 0,
            anchor
        };
    }

    restoreDetailViewport(content, state) {
        if (!content || !state) return;
        content.scrollTop = state.contentTop;
        if (typeof window.scrollTo === 'function') window.scrollTo(state.windowX, state.windowY);
        if (!state.anchor) return;
        const elements = state.anchor.type === 'book'
            ? [...content.querySelectorAll('[data-book-id]')]
            : [...content.querySelectorAll('[data-outcome]')];
        const anchor = elements.find(element => (state.anchor.type === 'book'
            ? element.dataset.bookId === state.anchor.value
            : element.dataset.outcome === state.anchor.value));
        if (!anchor || typeof window.scrollTo !== 'function') return;
        const delta = anchor.getBoundingClientRect().top - state.anchor.top;
        if (Number.isFinite(delta) && delta !== 0) {
            window.scrollTo(state.windowX, state.windowY + delta);
        }
    }

    handleAuthExpiry() {
        this.authEnabled = true;
        this.currentUser = null;
        this.resetSessionBoundState();
        this.stopAutoRefresh();
        this.showToast('Authentication required. Please log in.', 'error');
        this.redirectToLogin();
    }

    renderDetailsState(state, open) {
        if (!open || this.openSummary !== open) return;
        const container = document.getElementById('sync-summary-container');
        const content = document.getElementById('sync-summary-content');
        const tabs = document.getElementById('sync-summary-tabs');
        if (!container || !content || !tabs) return;
        container.style.display = 'block';
        tabs.innerHTML = `<button class="tab-button active" type="button">${this.escapeHtml(this.statuses[open.profileId]?.profile_name || `Profile ${open.profileId}`)}</button>`;
        const isError = state === 'error';
        content.innerHTML = `
            <div class="details-state" data-run-id="${this.escapeHtmlAttribute(open.runId)}" role="status" aria-live="polite">
                <p>${isError ? 'Run details could not be loaded.' : 'Loading run details…'}</p>
                ${isError ? `<button type="button" class="btn btn-secondary" data-details-retry>Retry</button>` : ''}
            </div>`;
    }

    renderDetailsStale(open, message = '') {
        if (!open || this.openSummary !== open) return;
        const summary = document.querySelector('#sync-summary-content .sync-summary');
        if (!summary || summary.dataset.runId !== open.runId) return;
        const content = document.getElementById('sync-summary-content');
        const viewport = content ? this.captureDetailViewport(content) : null;
        let state = summary.querySelector('[data-details-refresh-state]');
        if (!state) {
            state = document.createElement('div');
            state.dataset.detailsRefreshState = 'stale';
            state.className = 'details-refresh-state';
            summary.prepend(state);
        }
        state.setAttribute('role', 'status');
        state.setAttribute('aria-live', 'polite');
        state.innerHTML = `<span>Status may be stale${message ? `: ${this.escapeHtml(message)}` : ''}.</span> <button type="button" class="btn btn-sm" data-details-retry>Retry</button>`;
        this.restoreDetailViewport(content, viewport);
    }

    async fetchAndRenderDetails({ open = this.openSummary, preservePosition = false } = {}) {
        if (!open || open.loading) return;
        const requestGeneration = open.generation;
        const requestRunId = open.runId;
        const requestController = typeof AbortController === 'undefined' ? null : new AbortController();
        open.detailsController = requestController;
        open.loading = true;
        const content = document.getElementById('sync-summary-content');
        const container = document.getElementById('sync-summary-container');
        try {
            const { response, data: result } = await this.fetchJsonWithTimeout(
                `${this.profileUrl(open.profileId)}/runs/${encodeURIComponent(requestRunId)}/details`,
                { signal: requestController?.signal }
            );
            const snapshot = result.success ? result.data : result;
            if (response.status === 401 || response.status === 403) {
                this.handleAuthExpiry();
                return;
            }
            const currentRunId = this.statuses[open.profileId]?.snapshot?.run_id;
            const currentRequest = this.openSummary === open && open.generation === requestGeneration && open.runId === requestRunId;
            const replacingRun = !open.renderedRunId || open.renderedRunId !== requestRunId;
            if (!currentRequest) return;
            if (!response.ok || !snapshot || snapshot.run_id !== requestRunId || currentRunId !== requestRunId) {
                if (replacingRun) this.renderDetailsState('error', open);
                else this.renderDetailsStale(open, `refresh failed (${response.status})`);
                return;
            }
            const viewport = preservePosition && content ? this.captureDetailViewport(content) : null;
            if (viewport) open.viewport = viewport;
            const activeElement = document.activeElement;
            if (activeElement && content?.contains(activeElement)) {
                open.focus = {
                    bookId: activeElement.closest('[data-book-id]')?.dataset.bookId,
                    outcome: activeElement.closest('[data-outcome-category]')?.dataset.outcomeCategory,
                    tagName: activeElement.tagName,
                    className: activeElement.className
                };
            }
            this.renderDetailsSnapshot(snapshot);
            container.style.display = 'block';
            this.restoreDetailViewport(content, open.viewport);
            if (open.focus) {
                const candidates = content ? [...content.querySelectorAll(open.focus.tagName)] : [];
                const target = candidates.find(element => open.focus.bookId
                    ? element.closest('[data-book-id]')?.dataset.bookId === open.focus.bookId
                    : open.focus.outcome
                        ? element.dataset.outcomeCategory === open.focus.outcome
                        : element.className === open.focus.className);
                target?.focus({ preventScroll: true });
            }
        } catch (error) {
            const currentRequest = this.openSummary === open && open.generation === requestGeneration && open.runId === requestRunId;
            if (error.name !== 'AbortError' && currentRequest) {
                if (!open.renderedRunId || open.renderedRunId !== requestRunId) this.renderDetailsState('error', open);
                else this.renderDetailsStale(open, error.message);
                console.error('Error loading sync run details:', error);
            }
        } finally {
            open.loading = false;
            if (open.detailsController === requestController) open.detailsController = null;
        }
    }

    renderDetailsSnapshot(snapshot) {
        const open = this.openSummary;
        const content = document.getElementById('sync-summary-content');
        const tabs = document.getElementById('sync-summary-tabs');
        if (!open || !content || !tabs) return;
        if (!(open.expandedOutcomes instanceof Set)) open.expandedOutcomes = new Set();
        open.renderedRunId = snapshot.run_id;
        const categories = this.outcomeCategories(snapshot.outcome_counts || {});
        const records = new Map((snapshot.book_outcomes || []).map(record => [record.book_id, record]));
        const mismatches = new Map((snapshot.mismatches || [])
            .filter(mismatch => mismatch && mismatch.book_id != null)
            .map(mismatch => [String(mismatch.book_id), mismatch]));
        tabs.innerHTML = `<button class="tab-button active" type="button">${this.escapeHtml(this.statuses[open.profileId]?.profile_name || `Profile ${open.profileId}`)}</button>`;
        const snapshotState = String(snapshot.state || '').toLowerCase();
        const unresolved = Number(snapshot.outcome_counts?.needs_review || 0) + Number(snapshot.outcome_counts?.not_found || 0) + Number(snapshot.outcome_counts?.failed || 0);
        const groups = categories.map(category => {
            const groupRecords = [...records.values()].filter(record => record.outcome === category.key);
            return { ...category, records: groupRecords };
        });
        const groupsHtml = groups.map(group => `
            <details class="summary-section outcome-group" data-outcome="${group.key}" ${open.expandedOutcomes.has(group.key) ? 'open' : ''}>
                <summary data-outcome-category="${group.key}"><span>${group.label}</span><span class="stat ${group.tone}">${group.count}</span></summary>
                <div class="book-list">${group.records.length ? group.records.map(record => this.renderOutcomeRecord(
                    record,
                    record.outcome === 'needs_review' ? mismatches.get(String(record.book_id)) : null
                )).join('') : '<p class="empty-state">No books in this category.</p>'}</div>
            </details>`).join('');
        let statusMessage = 'Run status is unavailable.';
        if (snapshotState === 'completed') {
            statusMessage = unresolved === 0
                ? 'This run completed without unresolved or failed outcomes.'
                : `This run completed with ${unresolved} ${unresolved === 1 ? 'outcome that needs' : 'outcomes that need'} attention.`;
        } else if (snapshotState === 'failed') {
            statusMessage = 'This run failed before it could complete.';
        } else if (snapshotState === 'canceled') {
            statusMessage = 'This run was canceled.';
        } else if (snapshotState === 'syncing') {
            statusMessage = 'Currently syncing.';
        }
        const runError = this.statuses[open.profileId]?.terminal_error || '';
        content.innerHTML = `
            <div class="sync-summary" data-run-id="${this.escapeHtmlAttribute(snapshot.run_id)}">
                <div class="summary-header"><h3>Run details</h3><div class="last-sync">Started: ${new Date(snapshot.run_started_at).toLocaleString()}</div></div>
                <p class="status-message">${statusMessage}</p>
                ${runError ? `<div class="status-message status-error" data-run-error><strong>Run error:</strong> ${this.escapeHtml(runError)}</div>` : ''}
                <div class="summary-stats">${groups.map(group => `<div class="stat-item ${group.tone}"><span class="stat-value">${group.count}</span><span class="stat-label">${group.label}</span></div>`).join('')}</div>
                ${groupsHtml}
            </div>`;
        content.querySelectorAll('details.outcome-group').forEach(group => {
            group.addEventListener('toggle', () => {
                if (group.open) open.expandedOutcomes.add(group.dataset.outcome);
                else open.expandedOutcomes.delete(group.dataset.outcome);
            });
        });
    }

    renderHardcoverCandidate(mismatch) {
        if (!mismatch || typeof mismatch !== 'object') return '';

        const value = (raw) => {
            if (typeof raw === 'string') return raw.trim();
            if (typeof raw === 'number' && Number.isFinite(raw)) return String(raw);
            return '';
        };
        const title = value(mismatch.hardcover_title);
        const author = value(mismatch.hardcover_author);
        const publishedYear = value(mismatch.hardcover_published_year);
        const publisher = value(mismatch.hardcover_publisher);
        const asin = value(mismatch.hardcover_asin);
        const isbn = value(mismatch.hardcover_isbn);
        const slug = value(mismatch.hardcover_slug);
        // BookMismatch exposes edition metadata without a hardcover_ prefix;
        // keep the labels explicit because these fields describe the source
        // edition, while the hardcover_* fields above describe the candidate.
        const sourceFormat = value(mismatch.edition_format);
        const sourceEdition = value(mismatch.edition_information);
        const coverURL = value(mismatch.hardcover_cover_url);
        const hardcoverURL = slug
            ? `https://hardcover.app/books/${encodeURIComponent(slug)}`
            : '';
        const fields = [];
        const addField = (label, rawValue) => {
            const fieldValue = value(rawValue);
            if (fieldValue) {
                fields.push(`<span><strong>${label}:</strong> ${this.escapeHtml(fieldValue)}</span>`);
            }
        };

        if (title) {
            const titleHTML = hardcoverURL
                ? `<a href="${this.escapeHtmlAttribute(hardcoverURL)}" target="_blank" rel="noopener noreferrer">${this.escapeHtml(title)}</a>`
                : this.escapeHtml(title);
            fields.push(`<span><strong>Title:</strong> ${titleHTML}</span>`);
        }
        addField('Author', author);
        addField('Published', publishedYear);
        addField('Publisher', publisher);
        addField('ASIN', asin);
        addField('ISBN', isbn);
        addField('Slug', slug);
        addField('Source format', sourceFormat);
        addField('Source edition', sourceEdition);

        const coverIsHTTP = /^https?:\/\//i.test(coverURL) && !coverURL.includes('|');
        const coverHTML = coverIsHTTP
            ? `<img src="${this.escapeHtmlAttribute(coverURL)}"
                    data-fallbacks="${this.escapeHtmlAttribute(`${coverURL}|/cover-placeholder.svg`)}"
                    data-fb-idx="0"
                    alt="${this.escapeHtmlAttribute(`Hardcover cover${title ? ` for ${title}` : ''}`)}"
                    class="book-cover"
                    loading="lazy"
                    decoding="async"
                    onerror="window.__absHandleImageError && window.__absHandleImageError(this)">`
            : '';

        if (!fields.length && !coverHTML) return '';
        return `<section class="hardcover-candidate" aria-label="Hardcover candidate">
            <h4>Hardcover candidate</h4>
            ${coverHTML ? `<div>${coverHTML}</div>` : ''}
            ${fields.length ? `<div class="book-meta">${fields.join('')}</div>` : ''}
        </section>`;
    }

    renderOutcomeRecord(record, mismatch = null) {
        const bookId = String(record.book_id || '');
        return `<article class="book-item" data-book-id="${this.escapeHtmlAttribute(bookId)}">
            <div class="book-title">${this.escapeHtml(record.title || 'Unknown title')}</div>
            ${record.author ? `<div><strong>Author:</strong> ${this.escapeHtml(record.author)}</div>` : ''}
            <div class="book-meta">${record.asin ? `<span><strong>ASIN:</strong> ${this.escapeHtml(record.asin)}</span>` : ''}${record.isbn ? `<span><strong>ISBN:</strong> ${this.escapeHtml(record.isbn)}</span>` : ''}</div>
            ${record.match_method ? `<div><strong>Match method:</strong> ${this.escapeHtml(record.match_method)}</div>` : ''}
            ${mismatch ? this.renderHardcoverCandidate(mismatch) : ''}
            ${record.reason ? `<div class="book-reason"><strong>Reason:</strong> ${this.escapeHtml(record.reason)}</div>` : ''}
            ${record.error ? `<div class="book-error"><strong>Error:</strong> ${this.escapeHtml(record.error)}</div>` : ''}
        </article>`;
    }

    async handleAddProfile(event) {
        if (this.isViewer()) return;
        const formData = new FormData(event.target);
        const profileId = String(formData.get('id') || '');
        if (profileId.length > 244) {
            this.showToast('Profile ID must be 244 characters or fewer.', 'error');
            return;
        }
        const profileData = {
            id: profileId,
            name: formData.get('name'),
            audiobookshelf_url: formData.get('audiobookshelf_url'),
            audiobookshelf_token: formData.get('audiobookshelf_token'),
            hardcover_token: formData.get('hardcover_token'),
                sync_config: {
                    incremental: formData.get('incremental') === 'on',
                    min_change_threshold: 60,
                libraries: {
                    include: this.parseCommaSeparated(formData.get('include_libraries')),
                    exclude: this.parseCommaSeparated(formData.get('exclude_libraries'))
                },
                sync_interval: formData.get('sync_interval'),
                minimum_progress: parseFloat(formData.get('minimum_progress')),
                sync_want_to_read: formData.get('sync_want_to_read') === 'on',
                process_unread_books: formData.get('process_unread_books') === 'on',
                sync_owned: formData.get('sync_owned') === 'on',
                include_ebooks: formData.get('include_ebooks') === 'on',
                dry_run: formData.get('dry_run') === 'on',
                test_book_filter: '',
                test_book_limit: 0,
                audnexus_region: formData.get('audnexus_region') || ''
            }
        };
        const mutationKey = 'create-profile';
        const mutation = this.beginSessionMutation(mutationKey);

        try {
            this.showLoading();
            const response = await fetch('/api/profiles', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(profileData),
                ...(mutation.controller ? { signal: mutation.controller.signal } : {})
            });

            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            const data = await response.json();
            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;

            if (data.success) {
                this.showToast('Profile created successfully!', 'success');
                event.target.reset();
                this.loadProfiles();
                this.showTab('profiles');
            } else {
                this.showToast('Failed to create profile: ' + data.error, 'error');
            }
        } catch (error) {
            if (!this.isCurrentSessionMutation(mutationKey, mutation) || error.name === 'AbortError') return;
            this.showToast('Error creating profile: ' + error.message, 'error');
        } finally {
            if (this.finishSessionMutation(mutationKey, mutation) && this.sessionMutationRequests.size === 0) {
                this.hideLoading();
            }
        }
    }

    async editProfile(profileId) {
        if (this.isViewer()) return;
        const authGeneration = this.authSessionGeneration;
        this.editProfileRequest?.controller?.abort();
        const request = {
            controller: typeof AbortController === 'undefined' ? null : new AbortController()
        };
        this.editProfileRequest = request;
        const isCurrentRequest = () => this.authSessionGeneration === authGeneration
            && this.editProfileRequest === request
            && !request.controller?.signal.aborted;
        try {
            this.showLoading();
            
            // Check authentication status first
            if (this.authEnabled && !this.currentUser) {
                this.showToast('Please log in to edit profiles', 'error');
                this.redirectToLogin();
                return;
            }
            
            const response = await fetch(this.profileUrl(profileId), {
                method: 'GET',
                credentials: 'include', // Include session cookies
                headers: {
                    'Content-Type': 'application/json'
                },
                ...(request.controller ? { signal: request.controller.signal } : {})
            });

            if (!isCurrentRequest()) return;
            
            // Handle authentication errors specifically
            if (response.status === 401 || response.status === 403) {
                this.showToast('Authentication required. Please log in.', 'error');
                this.redirectToLogin();
                return;
            }
            
            if (!isCurrentRequest()) return;
            const data = await response.json();
            if (!isCurrentRequest()) return;

            if (response.ok && data.success) {
                this.currentEditUser = data.data;
                this.showEditModal();
            } else {
                // Handle different types of errors
                if (data.error && data.error.code === 'authentication_required') {
                    this.showToast('Authentication required. Please log in.', 'error');
                    this.redirectToLogin();
                } else if (response.status === 400) {
                    this.showToast('Invalid profile ID: ' + profileId, 'error');
                } else {
                    this.showToast('Failed to load profile data: ' + (data.error?.message || data.error || 'Unknown error'), 'error');
                }
            }
        } catch (error) {
            if (!isCurrentRequest() || error.name === 'AbortError') return;
            this.showToast('Error loading profile data: ' + error.message, 'error');
        } finally {
            if (this.editProfileRequest === request) {
                this.editProfileRequest = null;
                if (authGeneration === this.authSessionGeneration) this.hideLoading();
            }
        }
    }

    showEditModal() {
        const user = this.currentEditUser;
        const config = user.sync_config || {};
        
        // Basic user fields - use correct data structure from ProfileWithTokens
        document.getElementById('edit-user-id').value = user.profile.id;
        document.getElementById('edit-user-name').value = user.profile.name;
        document.getElementById('edit-abs-url').value = user.audiobookshelf_url;
        
        // Sync configuration fields
        document.getElementById('edit-incremental').checked = this.toBool(config.incremental, false);
        document.getElementById('edit-sync-interval').value = config.sync_interval || '6h';
        document.getElementById('edit-minimum-progress').value = config.minimum_progress || 0.01;
        document.getElementById('edit-sync-want-to-read').checked = this.toBool(config.sync_want_to_read, true);
        document.getElementById('edit-process-unread-books').checked = this.toBool(config.process_unread_books, true);
        document.getElementById('edit-sync-owned').checked = this.toBool(config.sync_owned, true);
        const includeEbooksEl = document.getElementById('edit-include-ebooks');
        if (includeEbooksEl) {
            includeEbooksEl.checked = this.toBool(config.include_ebooks, false);
        }
        document.getElementById('edit-dry-run').checked = this.toBool(config.dry_run, false);
        
        // Library filters
        const libraries = config.libraries || {};
        document.getElementById('edit-include-libraries').value = (libraries.include || []).join(', ');
        document.getElementById('edit-exclude-libraries').value = (libraries.exclude || []).join(', ');
        
        // Audnexus region
        document.getElementById('edit-audnexus-region').value = config.audnexus_region || '';
        
        document.getElementById('edit-user-modal').style.display = 'block';
    }

    closeEditModal() {
        const modal = document.getElementById('edit-user-modal');
        if (modal) {
            modal.style.display = 'none';
        }
        // Ensure loading overlay is hidden when modal is closed
        this.hideLoading();
        this.currentEditUser = null;
    }

    async handleEditProfile(event) {
        if (this.isViewer()) return;
        const formData = new FormData(event.target);
        const userId = formData.get('id');
        
        // Update user name
        const userUpdateData = {
            name: formData.get('name')
        };

        // Update user config with form data
        const configUpdateData = {
            audiobookshelf_url: formData.get('audiobookshelf_url'),
            audiobookshelf_token: formData.get('audiobookshelf_token') || this.currentEditUser.audiobookshelf_token,
            hardcover_token: formData.get('hardcover_token') || this.currentEditUser.hardcover_token,
                sync_config: {
                    incremental: formData.get('incremental') === 'on',
                    min_change_threshold: 60,
                libraries: {
                    include: this.parseCommaSeparated(formData.get('include_libraries')),
                    exclude: this.parseCommaSeparated(formData.get('exclude_libraries'))
                },
                sync_interval: formData.get('sync_interval'),
                minimum_progress: parseFloat(formData.get('minimum_progress')),
                sync_want_to_read: formData.get('sync_want_to_read') === 'on',
                process_unread_books: formData.get('process_unread_books') === 'on',
                sync_owned: formData.get('sync_owned') === 'on',
                include_ebooks: formData.get('include_ebooks') === 'on',
                dry_run: formData.get('dry_run') === 'on',
                test_book_filter: '',
                test_book_limit: 0,
                audnexus_region: formData.get('audnexus_region') || ''
            }
        };
        const mutationKey = 'edit-profile';
        const mutation = this.beginSessionMutation(mutationKey);

        try {
            this.showLoading();
            
            // Update user
            const userResponse = await fetch(this.profileUrl(userId), {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(userUpdateData),
                ...(mutation.controller ? { signal: mutation.controller.signal } : {})
            });

            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            const userData = await userResponse.json();
            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            if (!userData.success) {
                throw new Error(userData.error);
            }

            // Update config
            const configResponse = await fetch(this.profileUrl(userId, '/config'), {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(configUpdateData),
                ...(mutation.controller ? { signal: mutation.controller.signal } : {})
            });

            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            const configData = await configResponse.json();
            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            if (!configData.success) {
                throw new Error(configData.error);
            }

            this.showToast('Profile updated successfully!', 'success');
            this.closeEditModal();
            this.loadProfiles();
        } catch (error) {
            if (!this.isCurrentSessionMutation(mutationKey, mutation) || error.name === 'AbortError') return;
            this.showToast('Error updating profile: ' + error.message, 'error');
        } finally {
            if (this.finishSessionMutation(mutationKey, mutation) && this.sessionMutationRequests.size === 0) {
                this.hideLoading();
            }
        }
    }

    async deleteProfile(profileId) {
        if (this.isViewer()) return;
        if (!confirm('Are you sure you want to delete this sync profile? This action cannot be undone.')) {
            return;
        }
        const mutationKey = `delete-profile:${String(profileId)}`;
        const mutation = this.beginSessionMutation(mutationKey);

        try {
            this.showLoading();
            const response = await fetch(this.profileUrl(profileId), {
                method: 'DELETE',
                ...(mutation.controller ? { signal: mutation.controller.signal } : {})
            });

            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            const data = await response.json();
            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;

            if (data.success) {
                this.actionErrors.delete(profileId);
                this.showToast('Profile deleted successfully!', 'success');
                if (this.openSummary?.profileId === profileId) {
                    this.clearOpenSummary();
                }
                this.loadProfiles();
                this.loadStatuses();
            } else {
                this.showToast('Failed to delete profile: ' + (data.error || 'Unknown error'), 'error');
            }
        } catch (error) {
            if (!this.isCurrentSessionMutation(mutationKey, mutation) || error.name === 'AbortError') return;
            this.showToast('Error deleting profile: ' + error.message, 'error');
        } finally {
            if (this.finishSessionMutation(mutationKey, mutation) && this.sessionMutationRequests.size === 0) {
                this.hideLoading();
            }
        }
    }

    async startSync(profileId) {
        if (this.isViewer()) return;
        if (!profileId) {
            console.error('No profile ID provided for sync');
            this.showToast('Error: No profile ID provided', 'error');
            return;
        }
        const mutationKey = `start-sync:${String(profileId)}`;
        const mutation = this.beginSessionMutation(mutationKey);

        try {
            this.showLoading();
            const response = await fetch(this.profileUrl(profileId, '/sync'), {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                ...(mutation.controller ? { signal: mutation.controller.signal } : {})
            });

            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            const result = await response.json();
            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            
            if (response.ok) {
                this.actionErrors.delete(profileId);
                this.renderStatuses();
                this.showToast('Sync started successfully', 'success');
                // The action acknowledgement only contains a message; reload
                // the authoritative status before updating the card.
                await this.loadStatuses();
            } else {
                throw new Error(result.error || 'Failed to start sync');
            }
        } catch (error) {
            if (!this.isCurrentSessionMutation(mutationKey, mutation) || error.name === 'AbortError') return;
            console.error('Error starting sync:', error);
            this.actionErrors.set(profileId, { action: 'Start sync', message: error.message });
            this.renderStatuses();
            this.showToast(`Error: ${error.message}`, 'error');
        } finally {
            if (this.finishSessionMutation(mutationKey, mutation) && this.sessionMutationRequests.size === 0) {
                this.hideLoading();
            }
        }
    }

    async cancelSync(profileId) {
        if (this.isViewer()) return;
        if (!confirm('Are you sure you want to cancel the sync?')) {
            return;
        }

        if (!profileId) {
            console.error('No profile ID provided for cancel');
            this.showToast('Error: No profile ID provided', 'error');
            return;
        }
        const mutationKey = `cancel-sync:${String(profileId)}`;
        const mutation = this.beginSessionMutation(mutationKey);

        try {
            this.showLoading();
            const response = await fetch(this.profileUrl(profileId, '/sync'), {
                method: 'DELETE',
                ...(mutation.controller ? { signal: mutation.controller.signal } : {})
            });

            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            const result = await response.json();
            if (!this.isCurrentSessionMutation(mutationKey, mutation)) return;
            
            if (response.ok) {
                this.actionErrors.delete(profileId);
                this.renderStatuses();
                this.showToast('Sync cancelled', 'info');
                // The action acknowledgement only contains a message; reload
                // the authoritative status before updating the card.
                await this.loadStatuses();
            } else {
                throw new Error(result.error || 'Failed to cancel sync');
            }
        } catch (error) {
            if (!this.isCurrentSessionMutation(mutationKey, mutation) || error.name === 'AbortError') return;
            console.error('Error cancelling sync:', error);
            this.actionErrors.set(profileId, { action: 'Cancel sync', message: error.message });
            this.renderStatuses();
            this.showToast(`Error: ${error.message}`, 'error');
        } finally {
            if (this.finishSessionMutation(mutationKey, mutation) && this.sessionMutationRequests.size === 0) {
                this.hideLoading();
            }
        }
    }

    startAutoRefresh() {
        if (this.refreshInterval) return;

        // Refresh statuses every 5 seconds
        this.refreshInterval = setInterval(() => {
            if (this.autoRefreshEnabled &&
                (this.profileLoadFailed || document.getElementById('sync-tab').classList.contains('active'))) {
                if (this.activeStatusRequests > 0) return;
                if (this.profileLoadFailed && Date.now() < this.nextProfileRetryAt) return;
                this.loadStatuses({ silent: true });
            }
        }, 5000);
    }

    stopAutoRefresh() {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
            this.refreshInterval = null;
        }
    }

    toggleAutoRefresh() {
        this.autoRefreshEnabled = !this.autoRefreshEnabled;
        const button = document.getElementById('auto-refresh-toggle');
        if (button) {
            if (this.autoRefreshEnabled) {
                button.textContent = '⏸️ Pause Auto-Refresh';
                button.classList.remove('btn-warning');
                button.classList.add('btn-secondary');
            } else {
                button.textContent = '▶️ Resume Auto-Refresh';
                button.classList.remove('btn-secondary');
                button.classList.add('btn-warning');
            }
        }
    }

    showLoading() {
        const overlay = document.getElementById('loading-overlay');
        if (overlay) {
            overlay.classList.add('active');
            // Ensure the overlay is visible by setting display to flex
            overlay.style.display = 'flex';
        }
    }

    hideLoading() {
        const overlay = document.getElementById('loading-overlay');
        if (overlay) {
            overlay.classList.remove('active');
            // Hide the overlay completely after a short delay to allow for fade-out
            setTimeout(() => {
                if (overlay && !overlay.classList.contains('active')) {
                    overlay.style.display = 'none';
                }
            }, 300); // Match this with the CSS transition time
        }
    }

    showToast(message, type = 'info') {
        const container = document.getElementById('toast-container');
        const toast = document.createElement('div');
        toast.className = `toast ${type}`;
        toast.innerHTML = `
            ${this.escapeHtml(message)}
            <button class="toast-close" onclick="this.parentElement.remove()">&times;</button>
        `;

        container.appendChild(toast);

        // Auto-remove after 5 seconds
        setTimeout(() => {
            if (toast.parentElement) {
                toast.remove();
            }
        }, 5000);
    }

    parseCommaSeparated(value) {
        if (!value || value.trim() === '') {
            return [];
        }
        return value.split(',').map(item => item.trim()).filter(item => item.length > 0);
    }

    escapeHtml(text) {
        const div = document.createElement('div');
        div.textContent = text;
        return div.innerHTML;
    }

    escapeHtmlAttribute(text) {
        return this.escapeHtml(text).replace(/"/g, '&quot;').replace(/'/g, '&#39;');
    }
}

// Global functions for HTML onclick handlers
function showTab(tabName) {
    app.showTab(tabName);
}

function refreshUsers() {
    app.loadProfiles();
}

function refreshStatus() {
    app.loadStatuses();
}

function toggleAutoRefresh() {
    app.toggleAutoRefresh();
}

function togglePassword(inputId) {
    const input = document.getElementById(inputId);
    const button = input.nextElementSibling;
    
    if (input.type === 'password') {
        input.type = 'text';
        button.textContent = '🙈';
    } else {
        input.type = 'password';
        button.textContent = '👁️';
    }
}

function closeEditModal() {
    app.closeEditModal();
}

// Initialize the app when the page loads
let app;
document.addEventListener('DOMContentLoaded', () => {
    app = new SyncProfileApp();
    
    // Add event delegation for read more/less functionality
    document.addEventListener('click', (e) => {
        const readMoreLink = e.target.closest('.read-more');
        if (!readMoreLink) return;
        
        e.preventDefault();
        const container = readMoreLink.closest('.description-container');
        if (!container) return;
        
        const text = container.querySelector('.description-text');
        const fullText = container.querySelector('.description-full');
        
        if (text && fullText) {
            if (fullText.classList.contains('hidden')) {
                // Show full text
                text.classList.add('hidden');
                fullText.classList.remove('hidden');
                readMoreLink.textContent = 'Read less';
                
                // Scroll the full text into view if it's near the bottom of the viewport
                const containerRect = container.getBoundingClientRect();
                const viewportHeight = window.innerHeight || document.documentElement.clientHeight;
                
                if (containerRect.bottom > viewportHeight - 100) {
                    container.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
                }
            } else {
                // Show short text
                text.classList.remove('hidden');
                fullText.classList.add('hidden');
                readMoreLink.textContent = 'Read more';
                
                // Scroll the read more link into view if it's near the bottom
                const linkRect = readMoreLink.getBoundingClientRect();
                const viewportHeight = window.innerHeight || document.documentElement.clientHeight;
                
                if (linkRect.bottom > viewportHeight - 100) {
                    readMoreLink.scrollIntoView({ behavior: 'smooth', block: 'nearest' });
                }
            }
        }
    });
});

// Clean up on page unload
window.addEventListener('beforeunload', () => {
    if (app) {
        app.stopAutoRefresh();
    }
});
