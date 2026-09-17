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

function createApp() {
    const app = Object.create(SyncProfileApp.prototype);
    app.openSummary = null;
    app.actionErrors = new Map();
    app.statusRefreshError = null;
    app.authEnabled = false;
    app.currentUser = null;
    return app;
}

test('accepted queued run clears a prior terminal error from its status card', () => {
    const app = createApp();
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

    app.applyAcceptedRun('profile-1', {
        run_id: 'new-run',
        queued_at: '2026-09-17T12:00:00Z',
        message: 'Sync accepted and queued',
        dry_run: false
    });

    assert.equal(app.statuses['profile-1'].terminal_error, '');
    assert.doesNotMatch(app.renderStatusCard('profile-1', app.statuses['profile-1']), /previous run failed/);
});

test('active run timestamps distinguish current activity from the previous successful sync', () => {
    const app = createApp();
    const html = app.renderStatusCard('profile-1', {
        profile_id: 'profile-1',
        last_attempted_at: '2026-09-17T12:00:00Z',
        last_successful_at: '2026-09-16T12:30:00Z',
        snapshot: {
            run_id: 'run-1',
            state: 'running',
            queued_at: '2026-09-17T12:00:00Z',
            last_activity_at: '2026-09-17T12:05:00Z'
        }
    });

    assert.match(html, /<strong>Run started:<\/strong>/);
    assert.match(html, /<strong>Last activity:<\/strong>/);
    assert.match(html, /<strong>Previous successful sync:<\/strong>/);
    assert.doesNotMatch(html, /<strong>Last attempted:<\/strong>/);
    assert.doesNotMatch(html, /<strong>Last successful:<\/strong>/);
});

test('successful run timestamps show the run start and completion without historical duplicates', () => {
    const app = createApp();
    const html = app.renderStatusCard('profile-1', {
        profile_id: 'profile-1',
        last_attempted_at: '2026-09-17T12:00:00Z',
        last_successful_at: '2026-09-17T12:10:00Z',
        snapshot: {
            run_id: 'run-1',
            state: 'completed',
            queued_at: '2026-09-17T12:00:00Z',
            finished_at: '2026-09-17T12:10:00Z',
            dry_run: false
        }
    });

    assert.match(html, /<strong>Run started:<\/strong>/);
    assert.match(html, /<strong>Completed:<\/strong>/);
    assert.doesNotMatch(html, /<strong>Last attempted:<\/strong>/);
    assert.doesNotMatch(html, /<strong>Last successful:<\/strong>/);
});

for (const terminal of [
    { state: 'failed', label: 'Failed' },
    { state: 'canceled', label: 'Canceled' },
    { state: 'completed', label: 'Dry run completed', dry_run: true }
]) {
    test(`${terminal.state}${terminal.dry_run ? ' dry run' : ''} timestamps retain the prior successful sync`, () => {
        const app = createApp();
        const html = app.renderStatusCard('profile-1', {
            profile_id: 'profile-1',
            last_attempted_at: '2026-09-17T12:00:00Z',
            last_successful_at: '2026-09-16T12:30:00Z',
            snapshot: {
                run_id: 'run-1',
                state: terminal.state,
                queued_at: '2026-09-17T12:00:00Z',
                finished_at: '2026-09-17T12:10:00Z',
                dry_run: Boolean(terminal.dry_run)
            }
        });

        assert.match(html, new RegExp(`<strong>${terminal.label}:<\\/strong>`));
        assert.match(html, /<strong>Last successful:<\/strong>/);
        assert.doesNotMatch(html, /<strong>Last attempted:<\/strong>/);
    });
}

test('attempt and success timestamps remain as fallbacks without retained run details', () => {
    const app = createApp();
    const html = app.renderStatusCard('profile-1', {
        profile_id: 'profile-1',
        last_attempted_at: '2026-09-17T12:00:00Z',
        last_successful_at: '2026-09-16T12:30:00Z'
    });

    assert.match(html, /<strong>Last attempted:<\/strong>/);
    assert.match(html, /<strong>Last successful:<\/strong>/);
    assert.doesNotMatch(html, /<strong>Run started:<\/strong>/);
});

test('run details use the timestamp for the current lifecycle phase', () => {
    const app = createApp();
    const base = {
        queued_at: '2026-09-17T12:00:00Z',
        processing_started_at: '2026-09-17T12:01:00Z',
        last_activity_at: '2026-09-17T12:05:00Z',
        finished_at: '2026-09-17T12:10:00Z'
    };

    for (const expected of [
        { state: 'queued', label: 'Queued', timestamp: base.queued_at },
        { state: 'running', label: 'Running', timestamp: base.processing_started_at },
        { state: 'finalizing', label: 'Finalizing', timestamp: base.last_activity_at },
        { state: 'completed', label: 'Completed', timestamp: base.finished_at },
        { state: 'canceled', label: 'Canceled', timestamp: base.finished_at },
        { state: 'failed', label: 'Failed', timestamp: base.finished_at }
    ]) {
        assert.deepEqual(
            app.detailsStatusTimestamp({ ...base, state: expected.state }),
            { label: expected.label, timestamp: expected.timestamp }
        );
    }
});

test('run details fall back to the latest available earlier phase timestamp', () => {
    const app = createApp();

    assert.deepEqual(app.detailsStatusTimestamp({
        state: 'completed',
        queued_at: '2026-09-17T12:00:00Z',
        processing_started_at: '2026-09-17T12:01:00Z'
    }), {
        label: 'Completed',
        timestamp: '2026-09-17T12:01:00Z'
    });
});
