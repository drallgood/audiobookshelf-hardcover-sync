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
                return String(text).replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
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

test('completed dry-run details use the completed dry-run label', () => {
    const app = createApp();
    const snapshot = {
        state: 'completed',
        dry_run: true,
        queued_at: '2026-09-17T12:00:00Z',
        finished_at: '2026-09-17T12:10:00Z'
    };

    assert.deepEqual(app.detailsStatusTimestamp(snapshot), {
        label: 'Dry run completed',
        timestamp: snapshot.finished_at
    });
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

// ---- Edition creation and forget-match ----

function editionApp(overrides = {}) {
    const app = createApp();
    app.statuses = { p1: { snapshot: { run_id: 'run-1', state: 'completed' } } };
    app.trackedRunIds = new Map();
    app.showEditionDialog = () => {};
    app.loadStatuses = async () => {};
    app.showToast = () => {};
    app.openSummary = {
        profileId: 'p1',
        runContext: { runId: 'run-1', state: 'completed', dryRun: false },
        records: new Map()
    };
    return Object.assign(app, overrides);
}

const needsReview = {
    book_id: 'li_1', outcome: 'needs_review', title: '<b>Dune</b>', format: 'audiobook',
    asin: 'B00ABC1234', hardcover_book_id: '42'
};

test('create action requires an eligible needs-review record with an identifier and a permitted profile', () => {
    const app = editionApp();
    const ctx = app.openSummary.runContext;
    assert.equal(app.editionCreateIneligibleReason(needsReview, ctx), null);
    assert.match(app.editionCreateIneligibleReason({ ...needsReview, asin: '', isbn: '' }, ctx), /ASIN or ISBN/);
    assert.ok(app.editionCreateIneligibleReason({ ...needsReview, hardcover_book_id: '' }, ctx));
    assert.ok(app.editionCreateIneligibleReason({ ...needsReview, outcome: 'not_found' }, ctx));
    assert.ok(app.editionCreateIneligibleReason(needsReview, { ...ctx, state: 'running' }));

    assert.match(app.renderEditionActions(needsReview), /data-edition-action="add"/);
    app.currentUser = { role: 'viewer' };
    app.authEnabled = true;
    app.isViewer = () => true;
    assert.equal(app.renderEditionActions(needsReview), '');
});

test('create and forget are disabled with an explanation while the profile is syncing', () => {
    const app = editionApp();
    app.statuses.p1.snapshot.state = 'running';
    const add = app.renderEditionActions(needsReview);
    assert.match(add, /disabled/);
    assert.match(add, /sync is running/i);
    const matched = { book_id: 'li_2', outcome: 'synced', hardcover_book_id: '7', edition_id: '9' };
    assert.match(app.renderEditionActions(matched), /data-edition-action="forget"[^>]*disabled/);
});

test('matched items show their Hardcover target and a forget action', () => {
    const app = editionApp();
    const html = app.renderEditionActions({ book_id: 'li_2', outcome: 'already_current', hardcover_book_id: '7', edition_id: '9' });
    assert.match(html, /book 7, edition 9/);
    assert.match(html, /data-edition-action="forget"/);
});

test('capability gate blocks on known denial, warns when unverified, and passes when allowed', () => {
    const app = editionApp();
    const cap = {
        ebook: { status: 'allowed', can_attempt: true },
        audiobook: { status: 'denied', can_attempt: false, reason: 'no scope' }
    };
    assert.deepEqual(app.editionCapabilityGate(cap, 'audiobook'), { blocked: true, reason: 'no scope' });
    assert.deepEqual(app.editionCapabilityGate(cap, 'ebook'), { blocked: false, warning: '' });
    const unverified = app.editionCapabilityGate({ ebook: { status: 'unverified', can_attempt: true, warning: 'unverified!' } }, 'ebook');
    assert.equal(unverified.blocked, false);
    assert.equal(unverified.warning, 'unverified!');
});

test('Retry-After is honored and older servers fall back to a short wait', () => {
    const app = editionApp();
    assert.equal(app.retryAfterMs('7'), 7000);
    assert.equal(app.retryAfterMs(null), 2000);
    assert.equal(app.retryAfterMs('garbage'), 2000);
    const now = Date.parse('2026-09-27T12:00:00Z');
    assert.equal(app.retryAfterMs('Sun, 27 Sep 2026 12:00:05 GMT', now), 5000);
});

test('audiobook dialog shows region states, escapes ABS strings, and offers only the identifier edit', () => {
    const app = editionApp();
    const dialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false,
        capability: { audiobook: { status: 'unverified', can_attempt: true, warning: 'Permission unverified' }, dry_run: false },
        draft: {
            reading_format: 'audiobook', eligible: true, dry_run: false, region_status: 'unknown',
            source_identifiers: { asin: 'B00ABC1234' },
            audible_identifier_candidate: { asin: 'B00ABC1234' },
            metadata_preview: { title: '<img src=x onerror=alert(1)>' },
            warnings: [{ code: 'x', message: '<script>bad</script>' }]
        }
    };
    const html = app.renderEditionDialog(dialog);
    assert.doesNotMatch(html, /<script>|<img src=x|<b>Dune/);
    assert.match(html, /could not be confirmed/);
    assert.match(html, /Permission unverified/);
    assert.match(html, /name="audible_identifier"/);
    assert.doesNotMatch(html, /name="title"/);
    assert.match(html, /name="resync"/);

    const unavailable = app.renderEditionDialog({ ...dialog, draft: { ...dialog.draft, region_status: 'temporarily_unavailable' } });
    assert.match(unavailable, /temporarily unavailable/);
});

test('dry run is labelled and offers neither creation nor resync', () => {
    const app = editionApp();
    const html = app.renderEditionDialog({
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false,
        capability: null,
        draft: { reading_format: 'audiobook', eligible: true, dry_run: true, region_status: 'confirmed', confirmed_region: 'us', source_identifiers: {}, audible_identifier_candidate: { asin: 'B00ABC1234' } }
    });
    assert.match(html, /Dry run/);
    assert.doesNotMatch(html, /name="resync"/);
    assert.match(html, /data-edition-dialog="confirm-create"[^>]*disabled/);
});

test('known capability denial disables confirmation', () => {
    const app = editionApp();
    const html = app.renderEditionDialog({
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false,
        capability: { audiobook: { status: 'denied', can_attempt: false, reason: 'Token lacks scope' } },
        draft: { reading_format: 'audiobook', eligible: true, region_status: 'confirmed', confirmed_region: 'us', source_identifiers: {}, audible_identifier_candidate: { asin: 'B00ABC1234' } }
    });
    assert.match(html, /Token lacks scope/);
    assert.match(html, /data-edition-dialog="confirm-create"[^>]*disabled/);
});

test('create body sends only changed ebook fields and the opt-in resync flag', () => {
    const app = editionApp();
    const dialog = { runId: 'run-1', record: { book_id: 'li_9' }, draft: { reading_format: 'ebook' } };
    const fields = {
        title: { value: 'Same', original: 'Same' },
        asin: { value: 'B00NEW1234', original: '' },
        isbn_10: { value: '', original: '' },
        isbn_13: { value: '', original: '' }
    };
    assert.deepEqual(app.buildEditionCreateBody(dialog, fields, false), {
        run_id: 'run-1', abs_item_id: 'li_9', asin: 'B00NEW1234'
    });
    assert.equal(app.buildEditionCreateBody(dialog, fields, true).resync, true);

    const audio = { runId: 'run-1', record: { book_id: 'li_9' }, draft: { reading_format: 'audiobook' } };
    assert.deepEqual(app.buildEditionCreateBody(audio, { audible_identifier: { value: ' B00ABC1234:uk ', original: '' } }, false), {
        run_id: 'run-1', abs_item_id: 'li_9', audible_identifier: 'B00ABC1234:uk'
    });
});

function stubDialog(app, status, payload) {
    app.fetchJsonWithTimeout = async () => ({ response: { ok: status < 300, status, headers: { get: () => null } }, data: payload });
    app.readEditionFormFields = () => ({ fields: { audible_identifier: { value: 'B00ABC1234:us', original: '' } }, resync: true });
    app.editionDialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, busy: false, error: '', result: null,
        draft: { reading_format: 'audiobook', region_status: 'confirmed', dry_run: false }
    };
    return app.editionDialog;
}

test('successful create shows the outcome and a separate resync failure', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 200, { success: true, data: {
        status: 'created', hardcover_book_id: '42', hardcover_edition_id: '99',
        resync: { attempted: true, error: 'hardcover down' }
    } });
    await app.submitEditionCreate();
    const html = app.renderEditionDialog(dialog);
    assert.match(html, /edition was created/);
    assert.match(html, /resync failed: hardcover down/);
});

test('create failures surface permission denial and stale 409 clearly', async () => {
    const app = editionApp();
    let refreshed = 0;
    app.loadStatuses = async () => { refreshed++; };
    let dialog = stubDialog(app, 403, { success: false, error: 'Hardcover token is missing catalogue write permission' });
    await app.submitEditionCreate();
    assert.match(dialog.error, /Permission denied: Hardcover token is missing/);
    assert.equal(dialog.result, null);

    dialog = stubDialog(app, 409, { success: false, error: 'sync run no longer contains a usable needs-review source record' });
    await app.submitEditionCreate();
    assert.match(dialog.error, /no longer contains/);
    assert.match(dialog.error, /sync started after this page loaded/);
    assert.equal(refreshed, 1);
});

test('draft preview 429 defers retry by Retry-After and shows the wait', async () => {
    const app = editionApp();
    const dialog = { mode: 'create', profileId: 'p1', record: needsReview, busy: false, error: '', draft: null, capability: null, retryAt: 0 };
    app.editionDialog = dialog;
    app.fetchJsonWithTimeout = async (url) => url.includes('edition-capability')
        ? { response: { ok: true, status: 200 }, data: { success: true, data: {} } }
        : { response: { ok: false, status: 429, headers: { get: () => '30' } }, data: { success: false, error: 'busy' } };
    await app.loadEditionDraft();
    clearInterval(dialog.timer);
    assert.ok(dialog.retryAt - Date.now() > 25000);
    assert.match(app.renderEditionDialog(dialog), /Retry in \d+s/);
    assert.match(app.renderEditionDialog(dialog), /data-edition-dialog="retry"[^>]*disabled/);
});

test('forget confirmation explains rematching, is disabled in dry run, and reports the result', async () => {
    const app = editionApp();
    const record = { book_id: 'li_2', outcome: 'synced', title: 'T', hardcover_book_id: '7', edition_id: '9' };
    const dialog = { mode: 'forget', profileId: 'p1', record, capability: { dry_run: false }, busy: false, error: '', result: null };
    app.editionDialog = dialog;
    let html = app.renderEditionDialog(dialog);
    assert.match(html, /Nothing is deleted from Hardcover/);
    assert.match(html, /may select the same edition again/);
    assert.doesNotMatch(html, /confirm-forget"[^>]*disabled/);

    assert.match(app.renderEditionDialog({ ...dialog, capability: { dry_run: true } }), /confirm-forget"[^>]*disabled/);

    app.fetchJsonWithTimeout = async () => ({ response: { ok: true, status: 200 }, data: { success: true, data: { association_removed: true } } });
    await app.submitForget();
    assert.match(app.renderEditionDialog(dialog), /saved match was forgotten/);

    dialog.result = null;
    app.fetchJsonWithTimeout = async () => ({ response: { ok: false, status: 409 }, data: { success: false, error: 'Sync already in progress' } });
    await app.submitForget();
    assert.match(dialog.error, /Sync already in progress/);
});
