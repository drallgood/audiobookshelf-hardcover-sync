const assert = require('node:assert/strict');
const test = require('node:test');

global.window = {
    addEventListener() {},
    location: { pathname: '/' }
};
global.document = {
    addEventListener() {},
    createElement() {
        let text = '';
        return {
            set textContent(value) {
                text = value;
            },
            get innerHTML() {
                return text;
            }
        };
    }
};

const { SyncProfileApp } = require('./static/app.js');

test('accepted queued run clears a prior terminal error from its status card', () => {
    const app = Object.create(SyncProfileApp.prototype);
    app.users = [{ id: 'profile-1', name: 'Test Profile' }];
    app.statuses = {
        'profile-1': {
            profile_id: 'profile-1',
            profile_name: 'Test Profile',
            terminal_error: 'previous run failed',
            snapshot: { run_id: 'old-run', state: 'failed' }
        }
    };
    app.statusRefreshError = 'previous refresh failed';
    app.openSummary = null;
    app.actionErrors = new Map();
    app.authEnabled = false;
    app.currentUser = null;

    app.applyAcceptedRun('profile-1', {
        run_id: 'new-run',
        queued_at: '2026-09-17T12:00:00Z',
        message: 'Sync accepted and queued',
        dry_run: false
    });

    assert.equal(app.statuses['profile-1'].terminal_error, '');
    assert.doesNotMatch(app.renderStatusCard('profile-1', app.statuses['profile-1']), /previous run failed/);
});
