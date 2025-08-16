// Sync Profile Management App
console.info('Sync UI loaded', { build: '2025-08-16 01:05:44+02:00' });
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
        this.statuses = {};
        this.currentEditUser = null;
        this.refreshInterval = null;
        this.currentUser = null;
        this.authEnabled = false;
        this.hasRedirectedToLogin = false;
        
        this.init();
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
                // Load data in parallel for better performance
                await Promise.all([
                    this.loadProfiles(),
                    this.loadStatuses()
                ]);
                
                // Start auto-refresh only if we have data to refresh
                if (this.users.length > 0) {
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
                console.log('Auth enabled but no user, redirecting to login');
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
                    console.log('User authenticated:', this.currentUser);
                    return true;
                } else {
                    // Not authenticated but auth is enabled
                    this.currentUser = null;
                    console.log('User not authenticated, auth enabled:', this.authEnabled);
                    return false;
                }
            } else {
                // If we get an error, assume auth is enabled but user not authenticated
                this.currentUser = null;
                this.authEnabled = true;
                console.log('Auth error, assuming auth enabled');
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

            // Debug logging - show full user object for troubleshooting
            console.log('Current user object:', this.currentUser);
            console.log('Updating user info:', { 
                authEnabled: this.authEnabled, 
                currentUser: this.currentUser || 'No user',
                path: window.location.pathname
            });

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
            
        } catch (error) {
            console.error('Error updating user info:', error);
        }
    }

    async logout() {
        try {
            // Clear local state first to update UI immediately
            this.currentUser = null;
            this.authEnabled = true;
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
                // Clear any remaining data
                this.users = [];
                this.statuses = {};
                
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
                    <p>Click on "Add Profile" to create a new sync profile.</p>
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
                <div class="user-card">
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
                        
                        <div class="user-card-actions">
                            <button class="btn btn-sm btn-icon" onclick="app.editProfile('${this.escapeHtml(user.id)}')" title="Edit Profile">
                                <span class="icon">✏️</span> Edit
                            </button>
                            <button class="btn btn-sm btn-icon btn-danger" onclick="app.deleteProfile('${this.escapeHtml(user.id)}')" title="Delete Profile">
                                <span class="icon">🗑️</span> Delete
                            </button>
                            <button class="btn btn-sm btn-primary" onclick="app.startSync('${this.escapeHtml(user.id)}')" ${user.active ? '' : 'disabled'}>
                                <span class="icon">🔄</span> Sync Now
                            </button>
                        </div>
                    </div>
                </div>
            `;
        }).join('');
    }

    async loadProfiles() {
        try {
            this.showLoading();
            
            // Check authentication status first
            if (this.authEnabled && !this.currentUser) {
                this.showToast('Please log in to view profiles', 'error');
                this.redirectToLogin();
                return;
            }
            
            const response = await fetch('/api/profiles', {
                method: 'GET',
                credentials: 'include', // Include session cookies
                headers: {
                    'Content-Type': 'application/json'
                }
            });
            
            // Handle authentication errors specifically
            if (response.status === 401 || response.status === 403) {
                this.showToast('Authentication required. Please log in.', 'error');
                this.redirectToLogin();
                return;
            }
            
            const data = await response.json();

            if (response.ok && data.success) {
                this.users = data.data;
                this.renderProfiles();
            } else {
                // Handle different types of errors
                if (data.error && data.error.code === 'authentication_required') {
                    this.showToast('Authentication required. Please log in.', 'error');
                    this.redirectToLogin();
                } else {
                    this.showToast('Failed to load sync profiles: ' + (data.error?.message || data.error || 'Unknown error'), 'error');
                }
            }
        } catch (error) {
            this.showToast('Error loading sync profiles: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async loadStatuses() {
        try {
            this.showLoading();
            const statuses = {};
            const summaryPromises = [];
            
            // First, get the list of profiles if not already loaded
            if (!this.users || this.users.length === 0) {
                await this.loadProfiles();
            }
            
            // If no users, render empty status
            if (!this.users || this.users.length === 0) {
                this.statuses = {};
                this.renderStatuses();
                return;
            }
            
            // Fetch status for each profile
            for (const user of this.users) {
                try {
                    const statusResponse = await fetch(`/api/profiles/${user.id}/status`);
                    if (statusResponse.ok) {
                        const statusData = await statusResponse.json();
                        if (statusData.success) {
                            const hasSummary = statusData.data?.last_sync_summary || 
                                            (statusData.data?.books_synced !== undefined && 
                                             (statusData.data?.mismatches?.length > 0 || 
                                              statusData.data?.books_not_found?.length > 0));
                            
                            statuses[user.id] = {
                                ...statusData.data,
                                profile_id: user.id,
                                profile_name: user.name || `Profile ${user.id}`,
                                books_not_found: statusData.data?.books_not_found || [],
                                mismatches: statusData.data?.mismatches || [],
                                has_summary: hasSummary,
                                // Ensure we have the total books processed
                                books_total: statusData.data?.books_total || 0,
                                books_synced: statusData.data?.books_synced || 0,
                                last_sync: statusData.data?.last_sync
                            };
                            
                            // Always try to fetch the summary for completed/error states
                            if (statusData.data?.state === 'completed' || statusData.data?.state === 'error') {
                                summaryPromises.push(this.fetchSyncSummary(user.id, statuses));
                            }
                        // HC-specific: show correct notices and add extra fields
                        if (source === 'hc') {
                            const hasBookMatch = !!(cleanData.url || cleanData.slug || cleanData.path);
                            if (hasBookMatch) {
                                // We found a book via search (slug/path/url), but it's a mismatch (edition not matched)
                                details.push(`
                                    <div class="mt-2">
                                        <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-yellow-100 text-yellow-800">
                                            Edition not matched — showing closest book match
                                        </span>
                                    </div>
                                `);
                            } else {
                                // We couldn't even find a book match
                                details.push(`
                                    <div class="mt-2">
                                        <span class="inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium bg-red-100 text-red-800">
                                            Book not found on Hardcover
                                        </span>
                                    </div>
                                `);
                            }

                            // Extra HC metadata if present
                            if (typeof cleanData.average_rating === 'number' || typeof cleanData.rating === 'number') {
                                const r = (cleanData.average_rating ?? cleanData.rating).toString();
                                metadata.push({ label: 'Rating', value: this.escapeHtml(r) });
                            }
                            if (typeof cleanData.ratings_count === 'number') {
                                metadata.push({ label: 'Ratings', value: this.escapeHtml(cleanData.ratings_count.toString()) });
                            }
                            if (cleanData.slug) {
                                metadata.push({ label: 'Slug', value: this.escapeHtml(cleanData.slug) });
                            }
                            if (cleanData.series && typeof cleanData.series === 'string') {
                                metadata.push({ label: 'Series', value: this.escapeHtml(cleanData.series) });
                            }
                            const genres = cleanData.genres || cleanData.subjects;
                            if (Array.isArray(genres) && genres.length > 0) {
                                metadata.push({ label: 'Genres', value: genres.map(g => this.escapeHtml(String(g))).join(', ') });
                            }
                        }
                        }
                    }
                } catch (error) {
                    console.error(`Error fetching status for profile ${user.id}:`, error);
                }
            }
            
            // Wait for all summary fetches to complete
            await Promise.all(summaryPromises);
            
            this.statuses = statuses;
            this.renderStatuses();
            this.renderSyncSummary();
            
        } catch (error) {
            console.error('Error in loadStatuses:', error);
            this.showToast('Error loading statuses: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }
    
    async fetchSyncSummary(profileId, statuses) {
        try {
            console.log(`Fetching sync summary for profile ${profileId}...`);
            const response = await fetch(`/api/profiles/${profileId}/summary`);
            if (response.ok) {
                const result = await response.json();
                console.log('Raw sync summary response:', result);
                
                // The API returns the data directly in the response, not in a 'data' property
                const summaryData = result.success ? result.data || result : result;
                
                if (statuses[profileId]) {
                    const booksSynced = summaryData.books_synced || statuses[profileId].books_synced || 0;
                    const booksNotFound = summaryData.books_not_found || statuses[profileId].books_not_found || [];
                    const mismatches = summaryData.mismatches || statuses[profileId].mismatches || [];
                    const totalBooks = summaryData.total_books_processed !== undefined 
                        ? summaryData.total_books_processed 
                        : statuses[profileId].books_total || 0;
                    
                    // Update the status with the summary data
                    statuses[profileId] = {
                        ...statuses[profileId],
                        books_synced: booksSynced,
                        books_not_found: booksNotFound,
                        mismatches: mismatches,
                        has_summary: true,
                        books_total: totalBooks,
                        last_sync: statuses[profileId].last_sync || new Date().toISOString()
                    };
                    
                    console.log(`Updated sync summary for profile ${profileId}:`, statuses[profileId]);
                    
                    // Force a re-render of the statuses to show the updated summary
                    this.statuses = { ...statuses };
                }
            } else {
                console.error(`Failed to fetch summary for profile ${profileId}:`, response.status, response.statusText);
            }
        } catch (error) {
            console.error(`Error fetching summary for profile ${profileId}:`, error);
            // Don't show toast here to avoid multiple toasts for multiple failures
        }
    }
    
    renderStatuses() {
        const container = document.getElementById('sync-status');
        if (!container) return;

        if (Object.keys(this.statuses).length === 0) {
            container.innerHTML = `
                <div class="text-center" style="grid-column: 1 / -1; padding: 40px;">
                    <h3>No sync statuses available</h3>
                    <p>Add a new sync profile and start syncing to see status information.</p>
                </div>
            `;
            return;
        }

        // Debug: Log status data to console for troubleshooting
        console.log('Rendering statuses with data:', this.statuses);

        // Convert statuses object to array and filter out any null/undefined entries
        const statusArray = Object.entries(this.statuses).filter(([_, status]) => status);
        
        container.innerHTML = statusArray.map(([profileId, status]) => {
            const progress = status.progress || 0;
            const booksSynced = status.books_synced || 0;
            const booksTotal = status.books_total || 0;
            const booksNotFound = status.books_not_found?.length || 0;
            const mismatches = status.mismatches?.length || 0;
            
            // Determine if we should show the View Details button
            const hasSummary = status.has_summary || 
                             (status.status === 'completed' && 
                              (booksSynced > 0 || booksNotFound > 0 || mismatches > 0));
            
            const progressPercent = booksTotal > 0 ? Math.round((booksSynced / booksTotal) * 100) : 0;
            const lastSync = status.last_sync || status.lastSync || null;
            const statusText = status.status || 'idle';
            const profileName = status.profile_name || status.profile_id || 'Unknown Profile';

            return `
                <div class="status-card ${statusText.toLowerCase()}">
                    <div class="status-header">
                        <h3>${this.escapeHtml(profileName)}</h3>
                        <span class="status-badge">${statusText}</span>
                    </div>
                    <div class="status-info">
                        ${lastSync ? `
                            <div><strong>Last Sync:</strong> <span title="${new Date(lastSync).toLocaleString()}">${this.formatRelativeTime(lastSync)}</span></div>
                        ` : ''}
                        ${progress > 0 ? `
                            <div><strong>Progress:</strong> ${progress}%</div>
                        ` : ''}
                        ${booksTotal > 0 ? `
                            <div><strong>Books Processed:</strong> ${booksSynced} of ${booksTotal}</div>
                            <div class="progress-bar">
                                <div class="progress-fill" style="width: ${progressPercent}%"></div>
                            </div>
                        ` : ''}
                        ${hasSummary ? `
                            <div class="sync-summary-stats">
                                <span class="stat success">✓ ${booksSynced} synced</span>
                                ${booksNotFound > 0 ? `<span class="stat warning">⚠ ${booksNotFound} not found</span>` : ''}
                                ${mismatches > 0 ? `<span class="stat warning">⚠ ${mismatches} mismatches</span>` : ''}
                            </div>
                        ` : ''}
                        ${status.message ? `
                            <div class="status-message">${this.escapeHtml(status.message)}</div>
                        ` : ''}
                        ${status.error ? `
                            <div class="status-error">Error: ${this.escapeHtml(status.error)}</div>
                        ` : ''}
                    </div>
                    <div class="status-actions">
                        ${statusText.toLowerCase() === 'syncing' ? `
                            <button class="btn btn-warning" onclick="app.cancelSync('${profileId}')">
                                Cancel Sync
                            </button>
                        ` : `
                            <button class="btn btn-primary" onclick="app.startSync('${profileId}')">
                                ${statusText.toLowerCase() === 'error' ? 'Retry Sync' : 'Start Sync'}
                            </button>
                        `}
                        ${hasSummary ? `
                            <button class="btn btn-secondary" onclick="app.showSyncSummary('${profileId}')">
                                View Details
                            </button>
                        ` : ''}
                    </div>
                </div>
            `;
        }).join('');
    }
    
    showSyncSummary(profileId) {
        const status = this.statuses[profileId];
        if (!status) {
            console.error('No status found for profile:', profileId);
            return;
        }
        
        console.log('Showing sync summary for profile:', profileId, status);
        
        const container = document.getElementById('sync-summary-container');
        const content = document.getElementById('sync-summary-content');
        const tabsContainer = document.getElementById('sync-summary-tabs');
        
        if (!container || !content || !tabsContainer) {
            console.error('Missing required DOM elements for sync summary');
            return;
        }
        
        // Update the tabs to show the current profile
        tabsContainer.innerHTML = `
            <button class="tab-button active" data-profile="${profileId}">
                ${this.escapeHtml(status.profile_name || `Profile ${profileId}`)}
            </button>`;
        
        // Get the last sync time if available
        const lastSync = status.last_sync || status.lastSync;
        const lastSyncDate = lastSync ? new Date(lastSync).toLocaleString() : 'Never';
        
        // Build summary HTML
        const summary = status.last_sync_summary || status.lastSyncSummary || null;
        const booksSynced = (summary && typeof summary.books_synced === 'number') ? summary.books_synced : (status.books_synced || 0);
        const booksTotal = (summary && typeof summary.total_books_processed === 'number') ? summary.total_books_processed : (status.books_total || 0);
        // Prefer top-level mismatches; only fall back to summary mismatches if top-level is empty
        let mismatchesArr = Array.isArray(status.mismatches) && status.mismatches.length > 0
            ? status.mismatches
            : (Array.isArray(summary?.mismatches) ? summary.mismatches : []);
        // De-duplicate by book_id just in case both sources are present
        if (Array.isArray(summary?.mismatches) && summary.mismatches.length > 0 && Array.isArray(status.mismatches) && status.mismatches.length > 0) {
            const byId = new Map();
            [...status.mismatches, ...summary.mismatches].forEach(m => {
                const id = m.book_id || m.id;
                if (!byId.has(id)) byId.set(id, m);
            });
            mismatchesArr = Array.from(byId.values());
        }
        // Resolve the Audiobookshelf base URL for this profile (used to build ABS book links)
        const profileEntry = Array.isArray(this.users)
            ? this.users.find(u => (u.id || (u.profile && u.profile.id)) === profileId)
            : null;
        // Profiles returned by /api/profiles include Config as `config` with `audiobookshelf_url`
        const __absBaseUrl = profileEntry && profileEntry.config && profileEntry.config.audiobookshelf_url
            ? String(profileEntry.config.audiobookshelf_url).replace(/\/+$/, '')
            : '';
        let html = `
            <div class="sync-summary">
                <div class="summary-header">
                    <h3>Sync Summary: ${this.escapeHtml(status.profile_name || 'Unknown Profile')}</h3>
                    <div class="last-sync">Last Sync: ${lastSyncDate}</div>
                </div>
                <div class="summary-stats">
                    <div class="stat-item success">
                        <span class="stat-value">${booksSynced}</span>
                        <span class="stat-label">Books Synced</span>
                    </div>
                    <div class="stat-item info">
                        <span class="stat-value">${booksTotal}</span>
                        <span class="stat-label">Total Processed</span>
                    </div>`;
        
        // Add books not found stat if any
        if (status.books_not_found?.length > 0) {
            html += `
                    <div class="stat-item warning">
                        <span class="stat-value">${status.books_not_found.length}</span>
                        <span class="stat-label">Books Not Found</span>
                    </div>`;
        }
        
        // Add mismatches stat if any
        if (mismatchesArr.length > 0) {
            html += `
                    <div class="stat-item warning">
                        <span class="stat-value">${mismatchesArr.length}</span>
                        <span class="stat-label">Potential Mismatches</span>
                    </div>`;
        }
        
        html += `
                </div>`; // Close summary-stats
        
        // Add books not found section
        if (status.books_not_found?.length > 0) {
            const booksHtml = status.books_not_found.map(book => {
                const title = this.escapeHtml(book.title || 'Unknown Title');
                const subtitle = book.subtitle ? `<div class="book-subtitle">${this.escapeHtml(book.subtitle)}</div>` : '';
                const author = book.author ? `<div><strong>Author:</strong> ${this.escapeHtml(book.author)}</div>` : '';
                const publishedYear = book.published_year ? `<div><strong>Published:</strong> ${this.escapeHtml(book.published_year)}</div>` : '';
                const publisher = book.publisher ? `<div><strong>Publisher:</strong> ${this.escapeHtml(book.publisher)}</div>` : '';
                const asin = book.asin ? `<div><strong>ASIN:</strong> ${this.escapeHtml(book.asin)}</div>` : '';
                const isbn = book.isbn ? `<div><strong>ISBN:</strong> ${this.escapeHtml(book.isbn)}</div>` : '';
                const libraryId = book.library_id ? `<div><strong>Library ID:</strong> ${this.escapeHtml(book.library_id)}</div>` : '';
                const error = book.error ? `<div class="book-error">${this.escapeHtml(book.error)}</div>` : '';
                const reason = book.reason ? `<div class="book-reason"><strong>Reason:</strong> ${this.escapeHtml(book.reason)}</div>` : '';
                
                return `
                    <div class="book-item">
                        <div class="book-title">${title}</div>
                        ${subtitle}
                        <div class="book-meta">
                            ${author}
                            ${publishedYear}
                            ${publisher}
                            ${asin}
                            ${isbn}
                            ${libraryId}
                        </div>
                        ${error}
                        ${reason}
                    </div>`;
            }).join('');
            
            html += `
                <div class="summary-section">
                    <h4>Books Not Found in Hardcover</h4>
                    <div class="book-list">${booksHtml}
                    </div>
                </div>`;
        } else {
            html += `
                <div class="summary-section">
                    <p>All books were found in Hardcover.</p>
                </div>`;
        }
        
        // Add mismatches section if any
        if (mismatchesArr.length > 0) {
            const mismatchesHtml = mismatchesArr.map(mismatch => {
                // Get data from the mismatch object with proper fallbacks
                const absData = {
                    ...mismatch,
                    // Map any ABS-specific fields here if needed
                };
                // If ABS author is missing but Hardcover provided one, use Hardcover author as a fallback
                if ((!absData.author || absData.author === 'Unknown Author') && mismatch.hardcover_author) {
                    absData.author = mismatch.hardcover_author;
                }
                
                // Log mismatch data for debugging
                console.log('Mismatch data:', { absData, mismatch });
                
                // Extract author information
                const hardcoverAuthor = mismatch.hardcover_author || mismatch.author || 'Unknown Author';
                
                // Create direct link to the book on Hardcover (only if we have a book-level match via slug/path/url)
                const hardcoverBookUrl = (() => {
                    const data = mismatch.hardcover_data || {};
                    // Prefer explicit URL fields from backend
                    if (mismatch.hardcover_url && typeof mismatch.hardcover_url === 'string') return mismatch.hardcover_url;
                    if (data.url && typeof data.url === 'string') return data.url;
                    if (data.slug_url && typeof data.slug_url === 'string') return data.slug_url;
                    // Construct from slug or path
                    if (mismatch.hardcover_slug && typeof mismatch.hardcover_slug === 'string') return `https://hardcover.app/books/${mismatch.hardcover_slug}`;
                    if (data.slug && typeof data.slug === 'string') return `https://hardcover.app/books/${data.slug}`;
                    if (data.path && typeof data.path === 'string') return `https://hardcover.app${data.path}`;
                    // No book-level match -> no link
                    return '';
                })();

                // Base Hardcover data: ONLY what HC provided (no ABS fallbacks)
                const hcData = {
                    title: mismatch.hardcover_title || undefined,
                    author: mismatch.hardcover_author || undefined,
                    published_year: mismatch.hardcover_published_year || undefined,
                    publisher: mismatch.hardcover_publisher || undefined,
                    asin: mismatch.hardcover_asin || undefined,
                    isbn: mismatch.hardcover_isbn || undefined,
                    format: mismatch.hardcover_format || undefined,
                    language: mismatch.hardcover_language || undefined,
                    page_count: mismatch.hardcover_page_count || undefined,
                    description: mismatch.hardcover_description || undefined,
                    cover_url: mismatch.hardcover_cover_url || undefined,
                    id: mismatch.hardcover_book_id || undefined,
                    slug: mismatch.hardcover_slug || (mismatch.hardcover_data && mismatch.hardcover_data.slug) || undefined,
                    path: (mismatch.hardcover_data && mismatch.hardcover_data.path) || undefined,
                    // Include raw hardcover_data first so our computed URL can override legacy forms
                    ...(mismatch.hardcover_data || {}),
                    // Use the direct URL if available, otherwise construct it from slug/path only
                    url: hardcoverBookUrl
                };
                
                // Remove any duplicate or empty fields
                Object.keys(hcData).forEach(key => {
                    if (hcData[key] === undefined || hcData[key] === '') {
                        delete hcData[key];
                    }
                });

                // Normalize legacy Hardcover URL forms: prefer slug/path; otherwise drop link
                if (typeof hcData === 'object' && hcData) {
                    const legacyRe = /^https?:\/\/hardcover\.app\/book\/[0-9]+\/?$/i;
                    if (hcData.url && legacyRe.test(hcData.url)) {
                        if (hcData.slug) {
                            hcData.url = `https://hardcover.app/books/${hcData.slug}`;
                        } else if (hcData.path) {
                            hcData.url = `https://hardcover.app${hcData.path}`;
                        } else {
                            hcData.url = '';
                        }
                    }
                }
                
                // If we have a hardcover_book object, use its properties
                if (mismatch.hardcover_book) {
                    const book = mismatch.hardcover_book;
                    Object.assign(hcData, {
                        title: book.title || hcData.title,
                        author: book.author_display || book.author || hcData.author,
                        // Preserve authors array for multi-author rendering
                        authors: Array.isArray(book.authors) && book.authors.length > 0 ? book.authors : hcData.authors,
                        published_year: book.published_year || hcData.published_year,
                        publisher: book.publisher || hcData.publisher,
                        isbn: book.isbn || book.isbn13 || hcData.isbn,
                        format: book.format || hcData.format,
                        language: book.language || hcData.language,
                        page_count: book.page_count || hcData.page_count,
                        description: book.description || hcData.description,
                        cover_url: book.cover_url || book.cover_image_url || hcData.cover_url,
                        slug: book.slug || hcData.slug,
                        path: book.path || hcData.path,
                        // Prefer slug/path-derived URL or precomputed hcData.url over legacy /book/<id>
                        url: (
                            hcData.url ||
                            (book.slug ? `https://hardcover.app/books/${book.slug}` : (book.path ? `https://hardcover.app${book.path}` : '')) ||
                            book.url ||
                            hcData.url
                        )
                    });
                }
                
                const hasAbsData = absData && (absData.title || absData.author);
                const hasHcData = hcData && (hcData.title || hcData.author);
                
                const displayTitle = this.escapeHtml(absData?.title || 'Unknown Title');
                const displaySubtitle = absData?.subtitle ? this.escapeHtml(absData.subtitle) : '';
                
                // Helper function to clean and extract book data with deep fallbacks
                const extractBookData = (data, source) => {
                    if (!data) return {};
                    
                    // Clean the data by removing empty/undefined values and trimming strings
                    const cleanData = {};
                    Object.entries(data).forEach(([key, value]) => {
                        if (value !== undefined && value !== null && value !== '') {
                            if (typeof value === 'string') {
                                const trimmed = value.trim();
                                if (trimmed) cleanData[key] = trimmed;
                            } else if (Array.isArray(value) && value.length > 0) {
                                cleanData[key] = value;
                            } else if (typeof value === 'object' && value !== null) {
                                cleanData[key] = value;
                            } else if (value !== '') {
                                cleanData[key] = value;
                            }
                        }
                    });

                    // Extract and transform fields with fallbacks
                    const extracted = {
                        // Title with fallbacks
                        title: cleanData.title || cleanData.name || 'Unknown Title',
                        // Optional subtitle
                        subtitle: cleanData.subtitle,
                        
                        // Author with multiple fallback fields (include Hardcover's author_display)
                        author: cleanData.author || 
                               cleanData.author_display ||
                               cleanData.author_name || 
                               cleanData.authors?.[0]?.name ||
                               (Array.isArray(cleanData.authors) && cleanData.authors.length > 0 ? 
                                   cleanData.authors[0] : 'Unknown Author'),
                        
                        // Narrator (ABS)
                        narrator: cleanData.narrator || 
                                  cleanData.reader || 
                                  (Array.isArray(cleanData.narrators) ? cleanData.narrators.join(', ') : cleanData.narrators),
                        
                        // Published year from various date formats (prefer incoming published_year)
                        published_year: cleanData.published_year ||
                                      cleanData.publishedYear || 
                                      (cleanData.published_date ? 
                                          cleanData.published_date.split('-')[0] : 
                                          cleanData.publication_date?.split('-')[0]),
                        
                        // Publisher with fallback to series
                        publisher: cleanData.publisher || 
                                 (cleanData.series && cleanData.series.publisher) ||
                                 cleanData.series?.publisher,
                        
                        // Format with intelligent detection
                        format: cleanData.format || 
                               (cleanData.mediaType ? 
                                   `${cleanData.mediaType.charAt(0).toUpperCase()}${cleanData.mediaType.slice(1)}` : 
                                   source === 'abs' ? 'Audiobook' : 'Book'),
                        
                        // Language with fallback
                        language: cleanData.language || 
                                (cleanData.languages && cleanData.languages[0]) ||
                                cleanData.language_code,
                        
                        // Page count with fallbacks
                        page_count: cleanData.numPages || 
                                  cleanData.pageCount || 
                                  cleanData.pages,
                        
                        // Description with fallback to subtitle
                        description: cleanData.description || 
                                   cleanData.overview ||
                                   cleanData.summary,

                        // Duration fields (ABS)
                        duration_seconds: (typeof cleanData.duration_seconds === 'number' ? cleanData.duration_seconds :
                                           typeof cleanData.durationSeconds === 'number' ? cleanData.durationSeconds :
                                           (typeof cleanData.duration === 'number' ? cleanData.duration : undefined)),
                        duration: (typeof cleanData.duration === 'string' ? cleanData.duration :
                                   cleanData.length || cleanData.length_readable),
                        
                        // Cover image with multiple possible fields (accept raw cover_url too)
                        cover_url: cleanData.coverImageUrl || 
                                 cleanData.cover_image_url ||
                                 cleanData.cover_url ||
                                 cleanData.cover?.medium ||
                                 cleanData.cover?.large ||
                                 cleanData.image_url,
                        
                        // Identifiers with fallbacks
                        // Do NOT cross-fallback between ASIN and ISBN
                        asin: cleanData.asin,
                        isbn: cleanData.isbn || cleanData.isbn13 || cleanData.isbn_13 || cleanData.isbn10 || cleanData.isbn_10,
                        isbn10: cleanData.isbn10 || cleanData.isbn_10,
                        isbn13: cleanData.isbn13 || cleanData.isbn_13,
                        
                        // URLs with controlled generation: don't fall back to Amazon for title link
                        url: (() => {
                            const legacyRe = /^https?:\/\/hardcover\.app\/book\/[0-9]+\/?$/i;
                            // Start with provided URL when present
                            let u = cleanData.url || '';
                            // ABS keeps its abs_url
                            if (!u && source === 'abs' && cleanData.abs_url) u = cleanData.abs_url;
                            // For HC prefer slug/path when building from scratch
                            if (!u && source === 'hc') {
                                if (cleanData.slug && typeof cleanData.slug === 'string') u = `https://hardcover.app/books/${cleanData.slug}`;
                                else if (cleanData.path && typeof cleanData.path === 'string') u = `https://hardcover.app${cleanData.path}`;
                            }
                            // Normalize legacy HC book ID URLs
                            if (source === 'hc' && u && legacyRe.test(u)) {
                                if (cleanData.slug && typeof cleanData.slug === 'string') u = `https://hardcover.app/books/${cleanData.slug}`;
                                else if (cleanData.path && typeof cleanData.path === 'string') u = `https://hardcover.app${cleanData.path}`;
                                else u = '';
                            }
                            return u || '';
                        })(),
                        // Preserve ABS direct link for buttons and other UI
                        abs_url: cleanData.abs_url,
                        // Additional metadata
                        genres: cleanData.genres || cleanData.categories,
                        // Preserve authors array if provided (used for multi-author rendering)
                        authors: Array.isArray(cleanData.authors) && cleanData.authors.length > 0 ? cleanData.authors : undefined,
                        series: cleanData.series,
                        // ABS-specific additional metadata
                        library_id: cleanData.library_id,
                        folder_id: cleanData.folder_id,
                        release_date: cleanData.release_date,
                        // ABS-specific tracking/context
                        book_id: cleanData.book_id || cleanData.id,
                        timestamp: cleanData.timestamp,
                        created_at: cleanData.created_at,
                        reason: cleanData.reason,
                        attempts: cleanData.attempts,
                        
                        // Status information based on source
                        status: source === 'abs' ? 'In Audiobookshelf' : 'On Hardcover',
                        statusType: source === 'abs' ? 'success' : 'info'
                    };
                    
                    // Clean up any remaining undefined values
                    Object.keys(extracted).forEach(key => {
                        if (extracted[key] === undefined || 
                            (Array.isArray(extracted[key]) && extracted[key].length === 0)) {
                            delete extracted[key];
                        }
                    });
                    
                    return extracted;
                };
                
                // Build a list of cover image fallbacks (ordered, unique)
                const computeCoverFallbacks = (cleanData, rawData, source) => {
                    try {
                        const isHttp = (u) => typeof u === 'string' && /^https?:\/\//.test(u);

                        // Primary candidates from the current source
                        const candidates = [
                            cleanData && cleanData.cover_url,
                            cleanData && cleanData.image_url,
                            rawData && rawData.cover_url,
                            rawData && rawData.cover_image_url,
                            rawData && (rawData.cover?.large),
                            rawData && (rawData.cover?.medium)
                        ].filter(isHttp);

                        // If rendering ABS, append Hardcover URLs as fallbacks
                        if (source === 'abs' && typeof hcData === 'object' && hcData) {
                            const hcCandidates = [
                                hcData.cover_url,
                                hcData.cover_image_url,
                                hcData.image_url,
                                hcData.cover && hcData.cover.large,
                                hcData.cover && hcData.cover.medium
                            ].filter(isHttp);
                            candidates.push(...hcCandidates);
                        }

                        // Append local placeholder last
                        candidates.push('/cover-placeholder.svg');

                        // De-duplicate while preserving order
                        const seen = new Set();
                        const unique = [];
                        for (const u of candidates) {
                            if (!seen.has(u)) { seen.add(u); unique.push(u); }
                        }
                        return unique;
                    } catch (e) {
                        console.warn('computeCoverFallbacks error:', e);
                        return ['/cover-placeholder.svg'];
                    }
                };
                
                // Helper function to render a book's details
                const renderBookDetails = (data, source) => {
                    try {
                        console.log(`Rendering ${source} data:`, data);
                        if (!data || (typeof data === 'object' && Object.keys(data).length === 0)) {
                            console.log(`No data for ${source}`);
                            return `
                            <div class="comparison-details">
                                <div><em>No data available</em></div>
                            </div>`;
                        }
                        
                        // Extract and clean the data
                        const cleanData = extractBookData(data, source);
                        const details = [];
                        
                        // Set up author information with safe fallbacks and support for multiple authors
                        let authorToShow = (() => {
                            // Prefer explicit authors array when present (map objects to name)
                            if (Array.isArray(cleanData.authors) && cleanData.authors.length > 0) {
                                const names = cleanData.authors
                                    .map(a => (typeof a === 'string' ? a : (a && a.name ? a.name : '')))
                                    .filter(Boolean);
                                if (names.length > 0) return names.join(', ');
                            }
                            return cleanData.author || 'Unknown Author';
                        })();
                        
                        // HC-specific notices: show edition/book status ABOVE the image
                        if (source === 'hc') {
                            const hasBookMatch = !!(cleanData.url || cleanData.slug || cleanData.path || cleanData.id);
                            if (hasBookMatch) {
                                details.push(`
                                    <div class="mb-3">
                                        <div class="edition-warning rounded-md px-3 py-2 text-sm flex items-start gap-2">
                                            <i class="fas fa-exclamation-triangle mt-0.5 edition-warning-icon" aria-hidden="true"></i>
                                            <div>
                                                <div class="edition-warning-title"><strong>Edition not matched.</strong></div>
                                                <div class="edition-warning-text">Create or link the correct Hardcover edition.</div>
                                            </div>
                                        </div>
                                    </div>
                                `);
                            } else {
                                details.push(`
                                    <div class="mb-3">
                                        <div class="book-not-found rounded-md px-3 py-2 text-sm flex items-start gap-2">
                                            <i class="fas fa-info-circle mt-0.5 book-not-found-icon" aria-hidden="true"></i>
                                            <div>
                                                <div class="book-not-found-title"><strong>Book not found on Hardcover.</strong></div>
                                                <div class="book-not-found-text">Try searching on Hardcover to create it.</div>
                                            </div>
                                        </div>
                                    </div>
                                `);
                            }
                        }

                        // Add cover image near the top of each column
                        {
                            const fallbacks = computeCoverFallbacks(cleanData, data, source);
                            const initialSrc = fallbacks[0] || '/cover-placeholder.svg';
                            const fbAttr = this.escapeHtml(fallbacks.join('|'));
                            const altText = this.escapeHtml(cleanData.title || 'Book cover');
                            details.push(`
                                <div class="mb-3 text-center">
                                    <img src="${initialSrc}"
                                         data-fallbacks="${fbAttr}"
                                         data-fb-idx="0"
                                         alt="${altText}"
                                         class="book-cover mx-auto"
                                         loading="lazy"
                                         decoding="async"
                                         fetchpriority="low"
                                         onerror="window.__absHandleImageError && window.__absHandleImageError(this)"
                                         style="max-height: 300px; max-width: 100%; border-radius: 4px; box-shadow: 0 2px 8px rgba(0,0,0,0.1);">
                                </div>
                            `);
                        }
                        
                        // Use provided author URL only; do not fabricate cross-site search links
                        let authorUrl = cleanData.author_url;
                        
                        // Add title with link if URL is available
                        if (cleanData.title) {
                            const titleText = this.escapeHtml(cleanData.title);
                            
                            // Create title display with optional author
                            let titleHtml = `<div class="mb-2"><strong>Title</strong><span> `;
                            
                            if (cleanData.url) {
                                titleHtml += `<a href="${this.escapeHtml(cleanData.url)}" target="_blank" rel="noopener noreferrer" class="font-medium text-blue-600 hover:underline">${titleText} <i class="fas fa-external-link-alt" style="font-size: 0.8em;"></i></a>`;
                            } else {
                                titleHtml += titleText;
                            }
                            
                            // Add author if available
                            if (authorToShow && authorToShow !== 'Unknown Author') {
                                if (authorUrl) {
                                    titleHtml += ` <span class=\"text-gray-600\">by</span> <a href="${this.escapeHtml(authorUrl)}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline">${this.escapeHtml(authorToShow)} <i class=\"fas fa-external-link-alt\" style=\"font-size: 0.8em;\"></i></a>`;
                                } else {
                                    titleHtml += ` <span class=\"text-gray-600\">by</span> <span class=\"text-gray-800\">${this.escapeHtml(authorToShow)}</span>`;
                                }
                            }
                            
                            titleHtml += `</span></div>`;
                            details.push(titleHtml);
                            // Subtitle directly under the title, labeled like other fields
                            if (cleanData.subtitle) {
                                details.push(`<div class="mb-2"><strong>Subtitle</strong><span class="text-gray-800"> ${this.escapeHtml(cleanData.subtitle)}</span></div>`);
                            }
                        } else if (authorToShow && authorToShow !== 'Unknown Author') {
                            // If no title but we have an author, show just the author
                            if (authorUrl) {
                                details.push(`<div class=\"mb-2\"><strong>Author</strong><span> <a href="${this.escapeHtml(authorUrl)}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline">${this.escapeHtml(authorToShow)} <i class=\"fas fa-external-link-alt\" style=\"font-size: 0.8em;\"></i></a></span></div>`);
                            } else {
                                details.push(`<div class=\"mb-2\"><strong>Author</strong><span class=\"text-gray-800\">${this.escapeHtml(authorToShow)}</span></div>`);
                            }
                        }
                        
                        // Add metadata in a clean, consistent format
                        const metadata = [];
                        // For ABS (and HC), group metadata into sections for clarity
                        const identifiers = (source === 'abs' || source === 'hc') ? [] : null;
                        const metaSection = (source === 'abs' || source === 'hc') ? [] : null;
                        const tracking = (source === 'abs' || source === 'hc') ? [] : null;
                        
                        // Published: ABS shows date/year; HC only when an edition (release_date) exists
                        if (
                            (source !== 'hc' && (cleanData.release_date || cleanData.published_year)) ||
                            (source === 'hc' && !!cleanData.release_date)
                        ) {
                            const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                            const publishedVal = (cleanData.release_date || cleanData.published_year).toString();
                            target.push({
                                label: 'Published',
                                value: this.escapeHtml(publishedVal)
                            });
                        }
                        
                        // Add publisher if available
                        // - ABS: always show when present
                        // - HC: only show when an edition (release_date) is present
                        if (
                            (source === 'abs' && cleanData.publisher) ||
                            (source === 'hc' && cleanData.publisher && !!cleanData.release_date)
                        ) {
                            const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                            target.push({
                                label: 'Publisher',
                                value: this.escapeHtml(cleanData.publisher)
                            });
                        }
                        
                        // Omit format row; it's not meaningful for our comparison UI

                        // ABS-specific: add Narrator, ASIN, identifiers and tracking when present
                        if (source === 'abs') {
                            if (cleanData.narrator) {
                                metaSection.push({
                                    label: 'Narrator',
                                    value: this.escapeHtml(cleanData.narrator)
                                });
                            }
                            if (cleanData.asin) {
                                const asinEsc = this.escapeHtml(cleanData.asin);
                                const audibleHref = `https://www.audible.com/pd?asin=${asinEsc}`;
                                identifiers.push({
                                    label: 'ASIN',
                                    value: `<a href="${audibleHref}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline" title="Open on Audible"><code class="text-inherit">${asinEsc}</code> <i class="fas fa-external-link-alt" style="font-size: 0.8em;"></i></a>`
                                });
                            }
                            // Show ISBN fields if available
                            const isbnVal = data.isbn || cleanData.isbn;
                            const isbn10Val = data.isbn_10 || cleanData.isbn10 || cleanData.isbn_10;
                            const isbn13Val = data.isbn_13 || cleanData.isbn13 || cleanData.isbn_13;
                            if (isbnVal) {
                                identifiers.push({ label: 'ISBN', value: this.escapeHtml(isbnVal.toString()) });
                            }
                            if (isbn10Val) {
                                identifiers.push({ label: 'ISBN-10', value: this.escapeHtml(isbn10Val.toString()) });
                            }
                            if (isbn13Val) {
                                identifiers.push({ label: 'ISBN-13', value: this.escapeHtml(isbn13Val.toString()) });
                            }

                            // Show Library ID, Folder ID when available
                            if (data.library_id || cleanData.library_id) {
                                tracking.push({
                                    label: 'Library ID',
                                    value: `<code>${this.escapeHtml((data.library_id || cleanData.library_id).toString())}</code>`
                                });
                            }
                            if (data.folder_id || cleanData.folder_id) {
                                tracking.push({
                                    label: 'Folder ID',
                                    value: `<code>${this.escapeHtml((data.folder_id || cleanData.folder_id).toString())}</code>`
                                });
                            }
                            if (cleanData.book_id) {
                                const __bookIdStr = cleanData.book_id.toString();
                                // Prefer UI route with configured base URL
                                let __absBookUrl = __absBaseUrl
                                    ? `${__absBaseUrl}/item/${encodeURIComponent(__bookIdStr)}`
                                    : '';
                                // Fallback: use abs_url if provided by API
                                if (!__absBookUrl && typeof cleanData.abs_url === 'string' && cleanData.abs_url) {
                                    __absBookUrl = cleanData.abs_url;
                                }
                                // Fallback: derive UI link from cover_url (api/items/<id>/cover -> /audiobookshelf/item/<id>)
                                if (!__absBookUrl && typeof cleanData.cover_url === 'string' && cleanData.cover_url) {
                                    try {
                                        const u = new URL(cleanData.cover_url);
                                        const base = `${u.origin}/audiobookshelf`;
                                        __absBookUrl = `${base}/item/${encodeURIComponent(__bookIdStr)}`;
                                    } catch (_) {
                                        // ignore URL parse errors; leave as plain code
                                    }
                                }
                                const __valueHtml = __absBookUrl
                                    ? `<a href="${this.escapeHtml(__absBookUrl)}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline" title="Open in Audiobookshelf"><code class="text-inherit">${this.escapeHtml(__bookIdStr)}</code> <i class="fas fa-external-link-alt" style="font-size: 0.8em;"></i></a>`
                                    : `<code>${this.escapeHtml(__bookIdStr)}</code>`;
                                tracking.push({
                                    label: 'Book ID',
                                    value: __valueHtml
                                });
                            }
                            // Note: HC tracking is handled in the HC block below
                            // Remove Detected row as requested
                            if (typeof cleanData.attempts === 'number') {
                                tracking.push({
                                    label: 'Attempts',
                                    value: this.escapeHtml(String(cleanData.attempts))
                                });
                            }
                            // Remove Reason row as requested
                        }
                        // HC-specific tracking rows (mirrors ABS layout)
                        if (source === 'hc' && data && tracking) {
                            if (data.id) {
                                const __hcBookIdStr = data.id.toString();
                                // Build preferred HC link order: explicit url > slug > path
                                let __hcUrl = '';
                                if (typeof data.url === 'string' && data.url) {
                                    __hcUrl = data.url;
                                } else if (typeof data.slug === 'string' && data.slug) {
                                    __hcUrl = `https://hardcover.app/books/${encodeURIComponent(data.slug)}`;
                                } else if (typeof data.path === 'string' && data.path) {
                                    __hcUrl = `https://hardcover.app${data.path}`;
                                }
                                const __hcValueHtml = __hcUrl
                                    ? `<a href="${this.escapeHtml(__hcUrl)}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline" title="Open on Hardcover"><code class="text-inherit">${this.escapeHtml(__hcBookIdStr)}</code> <i class="fas fa-external-link-alt" style="font-size: 0.8em;"></i></a>`
                                    : `<code>${this.escapeHtml(__hcBookIdStr)}</code>`;
                                tracking.push({ label: 'Book ID', value: __hcValueHtml });
                            }
                        }
                        
                        // Add page count if available
                        if (cleanData.page_count) {
                            const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                            target.push({
                                label: 'Pages',
                                value: cleanData.page_count.toString()
                            });
                        }
                        
                        // Add language if available
                        if (cleanData.language) {
                            const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                            target.push({
                                label: 'Language',
                                value: this.escapeHtml(cleanData.language)
                            });
                        }
                        
                        // Add duration or length
                        if (cleanData.duration_seconds) {
                            const hours = Math.floor(cleanData.duration_seconds / 3600);
                            const minutes = Math.floor((cleanData.duration_seconds % 3600) / 60);
                            const durationText = hours > 0 ? `${hours}h ${minutes}m` : `${minutes}m`;
                            const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                            target.push({
                                label: 'Duration',
                                value: durationText
                            });
                        } else if (cleanData.duration) {
                            const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                            target.push({
                                label: 'Duration',
                                value: cleanData.duration
                            });
                        }
                        
                        // Add genres if available
                        if (cleanData.genres && cleanData.genres.length > 0) {
                            const genres = Array.isArray(cleanData.genres) 
                                ? cleanData.genres.map(g => this.escapeHtml(g)).join(', ')
                                : this.escapeHtml(cleanData.genres);
                            const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                            target.push({
                                label: 'Genres',
                                value: genres,
                                class: 'text-sm text-gray-600'
                            });
                        }
                        
                        // Add series information if available
                        if (cleanData.series) {
                            const seriesInfo = [];
                            if (cleanData.series.name) {
                                seriesInfo.push(`<span class="font-medium">${this.escapeHtml(cleanData.series.name)}</span>`);
                            }
                            if (cleanData.series.sequence) {
                                seriesInfo.push(`(Book ${cleanData.series.sequence})`);
                            }
                            
                            if (seriesInfo.length > 0) {
                                const target = (source === 'abs' || source === 'hc') ? metaSection : metadata;
                                target.push({
                                    label: 'Series',
                                    value: seriesInfo.join(' ')
                                });
                            }
                        }
                        
                        // Render metadata
                        if (source === 'abs' || (source === 'hc' && ((identifiers && identifiers.length) || (metaSection && metaSection.length) || (tracking && tracking.length)))) {
                            const sections = [
                                { title: 'Identifiers', items: identifiers || [] },
                                { title: 'Metadata', items: metaSection || [] },
                                { title: 'Tracking', items: tracking || [] }
                            ].filter(s => s.items && s.items.length > 0);

                            sections.forEach((section, idx) => {
                                // Divider between sections
                                if (idx > 0) {
                                    details.push(`<div class="my-2 border-t border-gray-200"></div>`);
                                }
                                // Section header
                                details.push(`
                                    <h4 class="mt-2 mb-1 text-xs font-semibold uppercase tracking-wide text-gray-500">${this.escapeHtml(section.title)}</h4>
                                `);
                                // Section items
                                section.items.forEach(item => {
                                    details.push(`
                                        <div>
                                            <strong>${item.label}</strong>
                                            <span class="${item.class || 'text-gray-800'}">${item.value}</span>
                                        </div>
                                    `);
                                });
                            });
                        } else {
                            // Non-ABS: flat list rendering
                            if (metadata.length > 0) {
                                metadata.forEach(item => {
                                    details.push(`
                                        <div>
                                            <strong>${item.label}</strong>
                                            <span class="${item.class || 'text-gray-800'}">${item.value}</span>
                                        </div>
                                    `);
                                });
                            }
                        }
                        
                        
                        
                        // Do not render a separate Author row if it was already shown with the title to avoid duplicates
                        // However, if there is no title, still show the author-only row
                        if (!cleanData.title && authorToShow && authorToShow !== 'Unknown Author') {
                            const authorText = this.escapeHtml(authorToShow);
                            if (authorUrl) {
                                details.push(`<div class=\"mb-2\"><strong>Author</strong><span> <a href="${this.escapeHtml(authorUrl)}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline">${authorText} <i class=\"fas fa-external-link-alt\" style=\"font-size: 0.8em;\"></i></a></span></div>`);
                            } else {
                                details.push(`<div class=\"mb-2\"><strong>Author</strong><span class=\"text-gray-800\">${authorText}</span></div>`);
                            }
                        }

                        // Do not add a separate HC button; title already links when available
                        
                        // Add Hardcover ID (HC side) to Identifiers section so HC matches ABS layout
                        if (source === 'hc' && data.id && identifiers) {
                            const hcIdLink = data.url ? data.url : (data.slug ? `https://hardcover.app/books/${data.slug}` : (data.path ? `https://hardcover.app${data.path}` : ''));
                            const idHtml = hcIdLink
                                ? `<a href="${hcIdLink}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline" title="Open on Hardcover"><code class="text-inherit">${this.escapeHtml(data.id)}</code> <i class="fas fa-external-link-alt" style="font-size: 0.8em;"></i></a>`
                                : `<code>${this.escapeHtml(data.id)}</code>`;
                            identifiers.push({ label: 'Hardcover ID', value: idHtml });
                        }
                        
                        // Add description if available (with markdown link support and read more/less)
                        if (cleanData.description) {
                            const maxLength = 300;
                            // First escape HTML, then handle markdown links
                            const escapeHtml = (str) => {
                                return str
                                    .replace(/&/g, '&amp;')
                                    .replace(/</g, '&lt;')
                                    .replace(/>/g, '&gt;')
                                    .replace(/"/g, '&quot;')
                                    .replace(/'/g, '&#039;');
                            };
                            
                            // Convert markdown links to HTML
                            const processMarkdownLinks = (text) => {
                                return text.replace(/\[([^\]]+)\]\(([^)]+)\)/g, 
                                    (match, text, url) => {
                                        return `<a href="${escapeHtml(url)}" target="_blank" rel="noopener noreferrer" class="text-blue-600 hover:underline">${escapeHtml(text)} <i class="fas fa-external-link-alt" style="font-size: 0.7em;"></i></a>`;
                                    }
                                );
                            };
                            
                            const escapedDescription = escapeHtml(cleanData.description);
                            const processedDescription = processMarkdownLinks(escapedDescription);
                            const isLong = processedDescription.length > maxLength;
                            
                            // Create short description by truncating at the last space before maxLength
                            let shortDesc = processedDescription;
                            if (isLong) {
                                const lastSpace = processedDescription.lastIndexOf(' ', maxLength);
                                shortDesc = processedDescription.substring(0, lastSpace > 0 ? lastSpace : maxLength) + '...';
                            }
                            
                            details.push(`
                                <div class="mt-3">
                                    <div class="font-medium text-gray-700 mb-1">Description:</div>
                                    <div class="text-gray-800 text-sm description-container">
                                        <span class="description-text">${shortDesc}</span>
                                        ${isLong ? 
                                            `<span class="description-full hidden">${processedDescription}</span>
                                            <a href="#" class="text-blue-600 hover:underline read-more">Read more</a>` 
                                            : ''
                                        }
                                    </div>
                                </div>
                            `);
                        }
                        
                        // Add action buttons in a consistent, accessible way
                        const buttons = [];
                        
                        // Do not add a separate Hardcover button; the title already links to Hardcover on the HC side
                        
                        // Removed external ASIN action button per request
                        
                        // Add Audiobookshelf button for ABS source
                        if (source === 'abs') {
                            const bookUrl = (cleanData.book_id && __absBaseUrl)
                                ? `${__absBaseUrl}/item/${encodeURIComponent(cleanData.book_id.toString())}`
                                : (cleanData.abs_url || cleanData.url || '');
                            if (bookUrl) {
                                buttons.push({
                                    url: bookUrl,
                                    icon: 'headphones',
                                    label: 'Open in Audiobookshelf',
                                    style: 'secondary',
                                    title: 'Open this book in Audiobookshelf'
                                });
                            }
                        }
                        
                        // Add Goodreads button only on ABS side (prefer ISBN13 > ISBN10 > ISBN)
                        if (source === 'abs') {
                            const grIsbn = cleanData.isbn13 || cleanData.isbn_13 || cleanData.isbn10 || cleanData.isbn_10 || cleanData.isbn;
                            if (grIsbn) {
                                buttons.push({
                                    url: `https://www.goodreads.com/search?q=${grIsbn}`,
                                    icon: 'goodreads',
                                    label: 'View on Goodreads',
                                    style: 'secondary',
                                    title: `Find ${cleanData.title || 'this book'} on Goodreads`
                                });
                            }
                        }
                        
                        // Render buttons with consistent styling
                        if (buttons.length > 0) {
                            const buttonClasses = {
                                primary: 'bg-indigo-600 hover:bg-indigo-700 text-white border-transparent',
                                secondary: 'bg-white hover:bg-gray-50 text-gray-700 border-gray-300',
                                danger: 'bg-red-600 hover:bg-red-700 text-white border-transparent'
                            };
                            
                            const iconMap = {
                                book: 'book',
                                amazon: 'amazon',
                                headphones: 'headphones',
                                goodreads: 'book-open',
                                external: 'external-link-alt'
                            };
                            
                            details.push(`
                                <div class="mt-4 flex flex-wrap gap-2">
                                    ${buttons.map(btn => `
                                        <a href="${this.escapeHtml(btn.url)}" 
                                           target="_blank" 
                                           rel="noopener noreferrer"
                                           title="${btn.title || ''}"
                                           class="inline-flex items-center px-3 py-1.5 border rounded-md text-xs font-medium shadow-sm focus:outline-none focus:ring-2 focus:ring-offset-2 focus:ring-indigo-500 ${buttonClasses[btn.style] || buttonClasses.secondary}">
                                            <i class="${btn.icon.startsWith('fa-') ? btn.icon : `fa${btn.icon === 'amazon' ? 'b' : 's'} fa-${iconMap[btn.icon] || iconMap.external}`} mr-1"></i> 
                                            ${btn.label}
                                        </a>
                                    `).join('\n')}
                                </div>
                            `);
                        }
                        
                        // Add status if available (e.g., "Not in Library")
                        if (data.status) {
                            const statusType = data.statusType || 'info';
                            details.push(`
                                <div class="mt-3">
                                    <span class="status-badge inline-flex items-center px-2.5 py-0.5 rounded-full text-xs font-medium ${
                                        statusType === 'success' ? 'bg-green-100 text-green-800' : 
                                        statusType === 'warning' ? 'bg-yellow-100 text-yellow-800' : 
                                        'bg-blue-100 text-blue-800'
                                    }">
                                        ${this.escapeHtml(data.status)}
                                    </span>
                                </div>
                            `);
                        }
                        
                        return `
                            <div class="comparison-details">
                                ${details.join('')}
                            </div>`;
                    } catch (error) {
                        console.error('Error in renderBookDetails:', error);
                        return '<div class="comparison-details"><em>Error loading details</em></div>';
                    }
                };
                
                // Do not inject status into Hardcover details to avoid duplicate display
                // The mismatch reason is already shown once below the comparison.
                
                // Generate the book details HTML first
                // Avoid rendering placeholder authors
                if (absData && absData.author === 'Unknown Author') {
                    delete absData.author;
                }

                const absDetails = renderBookDetails({
                    ...absData,
                    format: absData?.format || 'Audiobook', // Use format from data if available
                    // Do not pass a status to avoid extra badge rendering on ABS side
                    url: absData?.abs_url || absData?.link || absData?.url || '',
                    author: absData?.author || absData?.hardcover_author || absData?.author_name || 'Unknown Author'
                }, 'abs');
                
                // Resolve Hardcover author; if it's unknown, fall back to ABS author
                let hcAuthorResolved = hcData.author || hcData.hardcover_author || hcData.author_name;
                if (!hcAuthorResolved || hcAuthorResolved === 'Unknown Author') {
                    hcAuthorResolved = absData.author || mismatch.author || hcAuthorResolved;
                }
                if (hcAuthorResolved === 'Unknown Author') {
                    hcAuthorResolved = undefined;
                }

                const hcDetails = renderBookDetails({
                    ...hcData,
                    format: hcData.format || 'Book',
                    // Do not pass a status to prevent duplicate status badge in HC column
                    url: hcData.url || (hcData.id ? `https://hardcover.app/book/${hcData.id}` : ''),
                    author: hcAuthorResolved || 'Unknown Author'
                }, 'hc');
                
                // Determine ABS link for header title if available
                const __absHeaderUrl = absData?.abs_url || absData?.link || absData?.url || '';

                return `
                    <div class="mismatch-item" data-book-id="${mismatch.id || mismatch.book_id || 'book-' + Math.random().toString(36).substr(2, 9)}">
                        <div class="mismatch-header">
                            <div class="mismatch-title">${__absHeaderUrl ? `<a href="${this.escapeHtml(__absHeaderUrl)}" target="_blank" rel="noopener noreferrer" class="text-blue-700 hover:underline">${displayTitle} <i class=\"fas fa-external-link-alt\" style=\"font-size: 0.8em;\"></i></a>` : displayTitle}</div>
                            ${displaySubtitle ? `<div class=\"mismatch-subtitle\"><strong>Subtitle</strong><span class=\"text-gray-800\"> ${displaySubtitle}</span></div>` : ''}
                        </div>
                        
                        <div class="mismatch-columns mismatch-comparison">
                            <!-- Audiobookshelf Column -->
                            <div class="mismatch-col abs comparison-column">
                                <div class="mismatch-col-title abs comparison-header">
                                    <i class="fas fa-book-open"></i>
                                    <span>Audiobookshelf</span>
                                </div>
                                ${absDetails}
                            </div>
                            
                            <!-- Arrow Divider -->
                            <div class="mismatch-divider comparison-arrow">
                                <div class="mismatch-divider-dot">
                                    <i class="fas fa-arrow-right"></i>
                                </div>
                            </div>
                            
                            <!-- Hardcover Column -->
                            <div class="mismatch-col hc comparison-column">
                                <div class="mismatch-col-title hc comparison-header">
                                    <i class="fas fa-book"></i>
                                    <span>Hardcover</span>
                                </div>
                                ${hcDetails}
                            </div>
                        </div>
                        
                        ${mismatch.reason ? `
                            <div class="mismatch-reason">
                                <strong>Note:</strong> ${this.escapeHtml(mismatch.reason)}
                            </div>` : ''}
                    </div>`;
            }).join('');
            
            html += `
                <div class="summary-section">
                    <div class="section-header">
                        <h3>Potential Mismatches</h3>
                        <p class="mismatch-help">These books were found but may have some discrepancies. Please verify the details.</p>
                    </div>
                    <div class="summary-stats">
                        <div class="mismatches-container">
                            ${mismatchesHtml}
                        </div>
                    </div>
                </div>`;
        }
        
        // Close the sync-summary div
        html += `
            </div>`;
            
        // Update the content and show the container
        content.innerHTML = html;
        container.style.display = 'block';
        container.scrollIntoView({ behavior: 'smooth' });
        
        // Show the sync tab
        this.showTab('sync');
    }

    renderSyncSummary() {
        // Currently empty, but can be used to render a summary of all syncs
    }

    async handleAddProfile(event) {
        const formData = new FormData(event.target);
        const profileData = {
            id: formData.get('id'),
            name: formData.get('name'),
            audiobookshelf_url: formData.get('audiobookshelf_url'),
            audiobookshelf_token: formData.get('audiobookshelf_token'),
            hardcover_token: formData.get('hardcover_token'),
            sync_config: {
                incremental: formData.get('incremental') === 'on',
                state_file: `./data/${formData.get('id')}_sync_state.json`,
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
                dry_run: false,
                test_book_filter: '',
                test_book_limit: 0
            }
        };

        try {
            this.showLoading();
            const response = await fetch('/api/profiles', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(profileData)
            });

            const data = await response.json();

            if (data.success) {
                this.showToast('Profile created successfully!', 'success');
                event.target.reset();
                this.loadProfiles();
                this.showTab('profiles');
            } else {
                this.showToast('Failed to create profile: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error creating profile: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async editProfile(profileId) {
        try {
            this.showLoading();
            
            // Check authentication status first
            if (this.authEnabled && !this.currentUser) {
                this.showToast('Please log in to edit profiles', 'error');
                this.redirectToLogin();
                return;
            }
            
            const response = await fetch(`/api/profiles/${profileId}`, {
                method: 'GET',
                credentials: 'include', // Include session cookies
                headers: {
                    'Content-Type': 'application/json'
                }
            });
            
            // Handle authentication errors specifically
            if (response.status === 401 || response.status === 403) {
                this.showToast('Authentication required. Please log in.', 'error');
                this.redirectToLogin();
                return;
            }
            
            const data = await response.json();

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
            this.showToast('Error loading profile data: ' + error.message, 'error');
        } finally {
            this.hideLoading();
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
        document.getElementById('edit-incremental').checked = config.incremental || false;
        document.getElementById('edit-sync-interval').value = config.sync_interval || '6h';
        document.getElementById('edit-minimum-progress').value = config.minimum_progress || 0.01;
        document.getElementById('edit-sync-want-to-read').checked = config.sync_want_to_read !== false; // default true
        document.getElementById('edit-process-unread-books').checked = config.process_unread_books === true; // default false
        document.getElementById('edit-sync-owned').checked = config.sync_owned !== false; // default true
        
        // Library filters
        const libraries = config.libraries || {};
        document.getElementById('edit-include-libraries').value = (libraries.include || []).join(', ');
        document.getElementById('edit-exclude-libraries').value = (libraries.exclude || []).join(', ');
        
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
                state_file: `./data/${userId}_sync_state.json`,
                min_change_threshold: 60,
                libraries: {
                    include: this.parseCommaSeparated(formData.get('include_libraries')),
                    exclude: this.parseCommaSeparated(formData.get('exclude_libraries'))
                },
                sync_interval: formData.get('sync_interval'),
                minimum_progress: parseFloat(formData.get('minimum_progress')),
                sync_want_to_read: formData.get('sync_want_to_read') === 'on',
                sync_owned: formData.get('sync_owned') === 'on',
                dry_run: false,
                test_book_filter: '',
                test_book_limit: 0
            }
        };

        try {
            this.showLoading();
            
            // Update user
            const userResponse = await fetch(`/api/profiles/${userId}`, {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(userUpdateData)
            });

            const userData = await userResponse.json();
            if (!userData.success) {
                throw new Error(userData.error);
            }

            // Update config
            const configResponse = await fetch(`/api/profiles/${userId}/config`, {
                method: 'PUT',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(configUpdateData)
            });

            const configData = await configResponse.json();
            if (!configData.success) {
                throw new Error(configData.error);
            }

            this.showToast('Profile updated successfully!', 'success');
            this.closeEditModal();
            this.loadProfiles();
        } catch (error) {
            this.showToast('Error updating profile: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async deleteProfile(profileId) {
        if (!confirm('Are you sure you want to delete this sync profile? This action cannot be undone.')) {
            return;
        }

        try {
            this.showLoading();
            const response = await fetch(`/api/profiles/${profileId}`, {
                method: 'DELETE'
            });

            const data = await response.json();

            if (data.success) {
                this.showToast('Profile deleted successfully!', 'success');
                this.loadProfiles();
                this.loadStatuses();
            } else {
                this.showToast('Failed to delete profile: ' + (data.error || 'Unknown error'), 'error');
            }
        } catch (error) {
            this.showToast('Error deleting profile: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async startSync(profileId) {
        if (!profileId) {
            console.error('No profile ID provided for sync');
            this.showToast('Error: No profile ID provided', 'error');
            return;
        }

        try {
            this.showLoading();
            const response = await fetch(`/api/profiles/${profileId}/sync`, {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                }
            });

            const result = await response.json();
            
            if (response.ok) {
                this.showToast('Sync started successfully', 'success');
                // Update the specific profile status
                if (result.data) {
                    this.statuses[profileId] = {
                        ...result.data,
                        profile_id: profileId,
                        profile_name: this.statuses[profileId]?.profile_name || profileId
                    };
                    this.renderStatuses();
                } else {
                    // If no data in response, refresh all statuses
                    await this.loadStatuses();
                }
            } else {
                throw new Error(result.error || 'Failed to start sync');
            }
        } catch (error) {
            console.error('Error starting sync:', error);
            this.showToast(`Error: ${error.message}`, 'error');
            
            // Update UI to show error state
            if (profileId && this.statuses[profileId]) {
                this.statuses[profileId].status = 'error';
                this.statuses[profileId].error = error.message;
                this.renderStatuses();
            }
        } finally {
            this.hideLoading();
        }
    }

    async cancelSync(profileId) {
        if (!confirm('Are you sure you want to cancel the sync?')) {
            return;
        }

        if (!profileId) {
            console.error('No profile ID provided for cancel');
            this.showToast('Error: No profile ID provided', 'error');
            return;
        }

        try {
            this.showLoading();
            const response = await fetch(`/api/profiles/${profileId}/sync`, {
                method: 'DELETE'
            });

            const result = await response.json();
            
            if (response.ok) {
                this.showToast('Sync cancelled', 'info');
                // Update the specific profile status
                if (result.data) {
                    this.statuses[profileId] = {
                        ...result.data,
                        profile_id: profileId,
                        profile_name: this.statuses[profileId]?.profile_name || profileId,
                        status: 'cancelled'
                    };
                    this.renderStatuses();
                } else {
                    // If no data in response, refresh all statuses
                    await this.loadStatuses();
                }
            } else {
                throw new Error(result.error || 'Failed to cancel sync');
            }
        } catch (error) {
            console.error('Error cancelling sync:', error);
            this.showToast(`Error: ${error.message}`, 'error');
            
            // Update UI to show error state
            if (profileId && this.statuses[profileId]) {
                this.statuses[profileId].status = 'error';
                this.statuses[profileId].error = error.message;
                this.renderStatuses();
            }
        } finally {
            this.hideLoading();
        }
    }

    startAutoRefresh() {
        // Refresh statuses every 5 seconds
        this.refreshInterval = setInterval(() => {
            if (document.getElementById('sync-tab').classList.contains('active')) {
                this.loadStatuses();
            }
        }, 5000);
    }

    stopAutoRefresh() {
        if (this.refreshInterval) {
            clearInterval(this.refreshInterval);
            this.refreshInterval = null;
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
    app.init();
    
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
