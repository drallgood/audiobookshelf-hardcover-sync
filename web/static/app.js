// Sync Profile Management App
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
            const response = await fetch('/api/status');
            const data = await response.json();

            if (data.success) {
                this.statuses = data.data;
                this.renderStatuses();
            } else {
                this.showToast('Failed to load statuses: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error loading statuses: ' + error.message, 'error');
        }
    }

    renderProfiles() {
        const container = document.getElementById('users-list');
        
        if (this.users.length === 0) {
            container.innerHTML = `
                <div class="text-center" style="grid-column: 1 / -1; padding: 40px;">
                    <h3>No sync profiles found</h3>
                    <p>Add your first sync profile to get started with Audiobookshelf-Hardcover sync.</p>
                    <button class="btn btn-primary" onclick="app.showTab('add-user')">Add Sync Profile</button>
                </div>
            `;
            return;
        }

        container.innerHTML = this.users.map(user => `
            <div class="user-card">
                <div class="user-card-header">
                    <h3>${this.escapeHtml(user.name)}</h3>
                    <span class="user-id">${this.escapeHtml(user.id)}</span>
                </div>
                <div class="user-card-body">
                    <div class="user-info">
                        <div class="user-info-item">
                            <strong>Created:</strong>
                            <span>${new Date(user.created_at).toLocaleDateString()}</span>
                        </div>
                        </div>
                    </div>
                    <div class="user-actions">
                        <button class="btn btn-primary btn-small" onclick="app.editProfile('${user.id}')">
                            ✏️ Edit
                        </button>
                        <button class="btn btn-danger btn-small" onclick="app.deleteProfile('${user.id}')">
                            🗑️ Delete
                        </button>
                    </div>
                </div>
            </div>
        `).join('');
    }

    renderStatuses() {
        const container = document.getElementById('sync-status');
        
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
        console.log('Status data:', this.statuses);

        // Handle both array and object formats
        const statusArray = Array.isArray(this.statuses) ? this.statuses : Object.values(this.statuses);
        
        container.innerHTML = statusArray.map(status => `
            <div class="status-card ${status.status}">
                <div class="status-header">
                    <h3>${this.escapeHtml(status.profile_name)}</h3>
                    <span class="status-badge ${status.status}">${status.status}</span>
                </div>
                <div class="status-info">
                    ${status.last_sync ? `
                        <div><strong>Last Sync:</strong> ${new Date(status.last_sync).toLocaleString()}</div>
                    ` : ''}
                    ${status.progress ? `
                        <div><strong>Progress:</strong> ${this.escapeHtml(status.progress)}</div>
                    ` : ''}
                    ${status.books_total ? `
                        <div><strong>Books:</strong> ${status.books_synced}/${status.books_total}</div>
                        <div class="progress-bar">
                            <div class="progress-fill" style="width: ${(status.books_synced / status.books_total) * 100}%"></div>
                        </div>
                    ` : ''}
                </div>
                <div class="status-actions">
                    ${status.status === 'syncing' ? `
                        <button class="btn btn-warning" onclick="app.cancelSync('${status.profile_id}')">Cancel Sync</button>
                    ` : status.status === 'error' ? `
                        <button class="btn btn-danger" onclick="app.startSync('${status.profile_id}')">Retry Sync</button>
                    ` : `
                        <button class="btn btn-primary" onclick="app.startSync('${status.profile_id}')">Start Sync</button>
                    `}
                </div>
            </div>
        `).join('');
    }

// ...
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
        document.getElementById('edit-user-modal').style.display = 'none';
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

    async startSync(userId) {
        try {
            this.showLoading();
            const response = await fetch(`/api/profiles/${userId}/sync`, {
                method: 'POST'
            });

            const data = await response.json();

            if (data.success) {
                this.showToast('Sync started successfully!', 'success');
                this.loadStatuses();
            } else {
                this.showToast('Failed to start sync: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error starting sync: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async cancelSync(userId) {
        if (!confirm('Are you sure you want to cancel the sync?')) {
            return;
        }

        try {
            this.showLoading();
            const response = await fetch(`/api/profiles/${userId}/sync`, {
                method: 'DELETE'
            });

            const data = await response.json();

            if (data.success) {
                this.showToast('Sync cancelled successfully!', 'warning');
                this.loadStatuses();
            } else {
                this.showToast('Failed to cancel sync: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error cancelling sync: ' + error.message, 'error');
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
        document.getElementById('loading-overlay').classList.add('active');
    }

    hideLoading() {
        document.getElementById('loading-overlay').classList.remove('active');
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
});

// Clean up on page unload
window.addEventListener('beforeunload', () => {
    if (app) {
        app.stopAutoRefresh();
    }
});
