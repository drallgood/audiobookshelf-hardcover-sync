// Multi-User Management App
class MultiUserApp {
    constructor() {
        this.users = [];
        this.statuses = {};
        this.currentEditUser = null;
        this.refreshInterval = null;
        this.currentUser = null;
        this.authEnabled = false;
        
        this.init();
    }

    async init() {
        await this.checkAuthStatus();
        this.setupEventListeners();
        this.loadUsers();
        this.loadStatuses();
        this.startAutoRefresh();
    }

    async checkAuthStatus() {
        try {
            // Check if authentication is enabled by trying to access a protected endpoint
            const response = await fetch('/api/status', {
                method: 'GET',
                credentials: 'include'
            });
            
            if (response.status === 401) {
                // Authentication is enabled and user is not authenticated
                this.authEnabled = true;
                this.redirectToLogin();
                return;
            } else if (response.ok) {
                // Either auth is disabled or user is authenticated
                this.authEnabled = response.headers.get('X-Auth-Enabled') === 'true';
                if (this.authEnabled) {
                    await this.loadCurrentUser();
                }
            }
        } catch (error) {
            console.error('Error checking auth status:', error);
        }
        
        this.updateUserInfo();
    }

    async loadCurrentUser() {
        try {
            const response = await fetch('/api/auth/me', {
                method: 'GET',
                credentials: 'include'
            });
            
            if (response.ok) {
                const data = await response.json();
                if (data.success) {
                    this.currentUser = data.data;
                }
            } else if (response.status === 401) {
                // User is not authenticated
                this.currentUser = null;
                this.redirectToLogin();
            }
        } catch (error) {
            console.error('Error loading current user:', error);
            this.currentUser = null;
        }
    }

    redirectToLogin() {
        const currentPath = window.location.pathname + window.location.search;
        window.location.href = `/auth/login?redirect=${encodeURIComponent(currentPath)}`;
    }

    updateUserInfo() {
        const userInfoElement = document.getElementById('user-info');
        if (!userInfoElement) return;

        if (this.authEnabled && this.currentUser) {
            userInfoElement.innerHTML = `
                <div class="user-info">
                    <div class="user-avatar">${this.currentUser.username.charAt(0).toUpperCase()}</div>
                    <span>${this.currentUser.username}</span>
                </div>
                <button class="logout-btn" onclick="app.logout()">Logout</button>
            `;
        } else if (this.authEnabled) {
            userInfoElement.innerHTML = `
                <button class="logout-btn" onclick="app.redirectToLogin()">Login</button>
            `;
        } else {
            userInfoElement.innerHTML = '';
        }
    }

    async logout() {
        try {
            const response = await fetch('/auth/logout', {
                method: 'POST',
                credentials: 'include'
            });
            
            if (response.ok) {
                window.location.href = '/auth/login';
            } else {
                this.showToast('Logout failed', 'error');
            }
        } catch (error) {
            console.error('Logout error:', error);
            this.showToast('Logout failed', 'error');
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

        // Add user form
        document.getElementById('add-user-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.handleAddUser(e);
        });

        // Edit user form
        document.getElementById('edit-user-form').addEventListener('submit', (e) => {
            e.preventDefault();
            this.handleEditUser(e);
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
            this.loadUsers();
        } else if (tabName === 'sync') {
            this.loadStatuses();
        }
    }

    async loadUsers() {
        try {
            this.showLoading();
            const response = await fetch('/api/users');
            const data = await response.json();

            if (data.success) {
                this.users = data.data;
                this.renderUsers();
            } else {
                this.showToast('Failed to load users: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error loading users: ' + error.message, 'error');
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

    renderUsers() {
        const container = document.getElementById('users-list');
        
        if (this.users.length === 0) {
            container.innerHTML = `
                <div class="text-center" style="grid-column: 1 / -1; padding: 40px;">
                    <h3>No users found</h3>
                    <p>Add your first user to get started with multi-user sync.</p>
                    <button class="btn btn-primary" onclick="app.showTab('add-user')">Add User</button>
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
                        <div class="user-info-item">
                            <strong>Status:</strong>
                            <span class="status-badge ${user.active ? 'completed' : 'error'}">
                                ${user.active ? 'Active' : 'Inactive'}
                            </span>
                        </div>
                    </div>
                </div>
                <div class="user-card-actions">
                    <button class="btn btn-primary btn-small" onclick="app.editUser('${user.id}')">
                        ✏️ Edit
                    </button>
                    <button class="btn btn-success btn-small" onclick="app.startSync('${user.id}')">
                        ▶️ Sync
                    </button>
                    <button class="btn btn-danger btn-small" onclick="app.deleteUser('${user.id}')">
                        🗑️ Delete
                    </button>
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
                    <p>Add users and start syncing to see status information.</p>
                </div>
            `;
            return;
        }

        container.innerHTML = Object.values(this.statuses).map(status => `
            <div class="status-card ${status.status}">
                <div class="status-header">
                    <h3>${this.escapeHtml(status.user_name)}</h3>
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
                    ${status.error ? `
                        <div style="color: #dc3545;"><strong>Error:</strong> ${this.escapeHtml(status.error)}</div>
                    ` : ''}
                </div>
                <div class="status-actions">
                    ${status.status === 'syncing' ? `
                        <button class="btn btn-warning btn-small" onclick="app.cancelSync('${status.user_id}')">
                            ⏹️ Cancel
                        </button>
                    ` : `
                        <button class="btn btn-success btn-small" onclick="app.startSync('${status.user_id}')">
                            ▶️ Start Sync
                        </button>
                    `}
                </div>
            </div>
        `).join('');
    }

    async handleAddUser(event) {
        const formData = new FormData(event.target);
        const userData = {
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
                sync_owned: formData.get('sync_owned') === 'on',
                dry_run: false,
                test_book_filter: '',
                test_book_limit: 0
            }
        };

        try {
            this.showLoading();
            const response = await fetch('/api/users', {
                method: 'POST',
                headers: {
                    'Content-Type': 'application/json'
                },
                body: JSON.stringify(userData)
            });

            const data = await response.json();

            if (data.success) {
                this.showToast('User created successfully!', 'success');
                event.target.reset();
                this.loadUsers();
                this.showTab('users');
            } else {
                this.showToast('Failed to create user: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error creating user: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async editUser(userId) {
        try {
            this.showLoading();
            const response = await fetch(`/api/users/${userId}`);
            const data = await response.json();

            if (data.success) {
                this.currentEditUser = data.data;
                this.showEditModal();
            } else {
                this.showToast('Failed to load user data: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error loading user data: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    showEditModal() {
        const user = this.currentEditUser;
        const config = user.sync_config || {};
        
        // Basic user fields
        document.getElementById('edit-user-id').value = user.user.id;
        document.getElementById('edit-user-name').value = user.user.name;
        document.getElementById('edit-abs-url').value = user.audiobookshelf_url;
        
        // Sync configuration fields
        document.getElementById('edit-incremental').checked = config.incremental || false;
        document.getElementById('edit-sync-interval').value = config.sync_interval || '6h';
        document.getElementById('edit-minimum-progress').value = config.minimum_progress || 0.01;
        document.getElementById('edit-sync-want-to-read').checked = config.sync_want_to_read !== false; // default true
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

    async handleEditUser(event) {
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
            const userResponse = await fetch(`/api/users/${userId}`, {
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
            const configResponse = await fetch(`/api/users/${userId}/config`, {
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

            this.showToast('User updated successfully!', 'success');
            this.closeEditModal();
            this.loadUsers();
        } catch (error) {
            this.showToast('Error updating user: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async deleteUser(userId) {
        if (!confirm('Are you sure you want to delete this user? This action cannot be undone.')) {
            return;
        }

        try {
            this.showLoading();
            const response = await fetch(`/api/users/${userId}`, {
                method: 'DELETE'
            });

            const data = await response.json();

            if (data.success) {
                this.showToast('User deleted successfully!', 'success');
                this.loadUsers();
                this.loadStatuses();
            } else {
                this.showToast('Failed to delete user: ' + data.error, 'error');
            }
        } catch (error) {
            this.showToast('Error deleting user: ' + error.message, 'error');
        } finally {
            this.hideLoading();
        }
    }

    async startSync(userId) {
        try {
            this.showLoading();
            const response = await fetch(`/api/users/${userId}/sync`, {
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
            const response = await fetch(`/api/users/${userId}/sync`, {
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
    app.loadUsers();
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
    app = new MultiUserApp();
});

// Clean up on page unload
window.addEventListener('beforeunload', () => {
    if (app) {
        app.stopAutoRefresh();
    }
});
