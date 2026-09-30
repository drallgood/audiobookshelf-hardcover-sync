const assert = require('node:assert/strict');
const test = require('node:test');

global.window = {
    addEventListener() {},
    location: { pathname: '/' },
    sessionStorage: (() => {
        const values = new Map();
        return {
            getItem(key) { return values.get(key) ?? null; },
            setItem(key, value) { values.set(key, String(value)); },
            removeItem(key) { values.delete(key); },
            clear() { values.clear(); }
        };
    })()
};
global.document = {
    addEventListener() {},
    getElementById() { return null; },
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

function pendingDialogApp() {
    const app = createApp();
    app.actionErrors = new Map();
    app.terminalErrorCache = new Map();
    app.terminalErrorRetries = new Map();
    app.terminalErrorRequests = new Map();
    app.trackedRunIds = new Map();
    app.sessionMutationRequests = new Map();
    app.statusRefreshWaiters = [];
    app.users = [{ id: 'private-profile' }];
    app.statuses = { 'private-profile': { private: true } };
    app.statusRefreshError = 'private status';
    app.statusLoadController = null;
    app.authSessionGeneration = 10;
    app.statusLoadSequence = 20;
    app.openSummary = { private: true };
    app.editProfileRequest = null;
    app.resetProfileRetry = () => {};
    app.closeEditModal = () => {};
    app.clearOpenSummary = () => { app.openSummary = null; };
    app.renderProfiles = () => {};
    app.renderStatuses = () => {};
    app.handleAuthExpiry = () => { app.authExpiryCalls = (app.authExpiryCalls || 0) + 1; };
    app.showToast = () => { app.toastCalls = (app.toastCalls || 0) + 1; };

    const modal = { style: { display: 'block' } };
    let html = 'private dialog';
    let htmlWrites = 0;
    const content = {
        get innerHTML() { return html; },
        set innerHTML(value) { html = value; htmlWrites++; },
        replaceChildren() { html = ''; }
    };
    const previousDocument = global.document;
    global.document = {
        getElementById(id) { return id === 'edition-modal' ? modal : id === 'edition-modal-content' ? content : null; },
        createElement(...args) { return previousDocument.createElement(...args); },
        querySelector() { return null; }
    };
    return {
        app, modal, content,
        get htmlWrites() { return htmlWrites; },
        restore() { global.document = previousDocument; }
    };
}

function deferred() {
    let resolve;
    const promise = new Promise(done => { resolve = done; });
    return { promise, resolve };
}

test('session reset closes a pending edition preview and ignores its late response', async t => {
    const harness = pendingDialogApp();
    t.after(harness.restore);
    const { app, modal, content } = harness;
    const pending = [];
    app.fetchJsonWithTimeout = url => {
        const request = deferred();
        pending.push({ url, ...request });
        return request.promise;
    };
    app.editionDialog = {
        mode: 'create', profileId: 'private-profile', record: { book_id: 'li_1', title: 'Private title' },
        draft: null, loading: true, busy: false, error: '', retryAt: 0
    };
    const loading = app.loadEditionDraft();
    const previewController = app.editionDialog.controller;

    app.resetSessionBoundState();
    assert.equal(app.editionDialog, null);
    assert.equal(app.authSessionGeneration, 11);
    assert.equal(app.statusLoadSequence, 21);
    assert.deepEqual(app.users, []);
    assert.deepEqual(Object.keys(app.statuses), []);
    assert.equal(app.openSummary, null);
    assert.equal(previewController.signal.aborted, true);
    assert.equal(content.innerHTML, '');
    assert.equal(modal.style.display, 'none');

    const newSessionDialog = { mode: 'forget', record: { book_id: 'new-session-item' } };
    app.editionDialog = newSessionDialog;
    content.innerHTML = 'new session dialog';
    modal.style.display = 'block';
    const writesForNewSession = harness.htmlWrites;

    pending.find(item => item.url.includes('/edition-drafts/source/')).resolve({
        response: { ok: true, status: 200 }, data: { success: true, data: { private: 'draft' } }
    });
    pending.find(item => item.url.includes('/edition-capability')).resolve({ response: { ok: true, status: 200 }, data: { success: true, data: {} } });
    await loading;
    assert.equal(app.editionDialog, newSessionDialog);
    assert.equal(harness.htmlWrites, writesForNewSession);
    assert.equal(content.innerHTML, 'new session dialog');
    assert.equal(app.authExpiryCalls || 0, 0);
});

test('session reset ignores a late successful create without toast or dialog redraw', async t => {
    const harness = pendingDialogApp();
    t.after(harness.restore);
    const { app, content } = harness;
    const request = deferred();
    app.fetchJsonWithTimeout = () => request.promise;
    app.readEditionFormFields = () => ({});
    app.editionDialog = {
        mode: 'create', profileId: 'private-profile', runId: 'run-1', record: needsReview,
        draft: { reading_format: 'audiobook', dry_run: false }, busy: false, error: '', result: null
    };
    const submitting = app.submitEditionCreate();
    const writesBeforeReset = harness.htmlWrites;
    app.resetSessionBoundState();
    request.resolve({ response: { ok: true, status: 200 }, data: { success: true, data: { private: 'result' } } });
    await submitting;
    assert.equal(app.editionDialog, null);
    assert.equal(app.toastCalls || 0, 0);
    assert.equal(app.authExpiryCalls || 0, 0);
    assert.equal(harness.htmlWrites, writesBeforeReset);
    assert.equal(content.innerHTML, '');
});

test('closing a pending create is ignored and its successful result is processed', async t => {
    const harness = pendingDialogApp();
    t.after(harness.restore);
    const { app } = harness;
    const request = deferred();
    app.fetchJsonWithTimeout = () => request.promise;
    app.readEditionFormFields = () => ({});
    app.refreshEditionActionStates = () => { app.actionRefreshes = (app.actionRefreshes || 0) + 1; };
    app.openSummary = { profileId: 'private-profile', runContext: { runId: 'run-1' }, addedEditionBookIds: new Set() };
    const dialog = {
        mode: 'create', profileId: 'private-profile', runId: 'run-1', record: { ...needsReview },
        draft: { reading_format: 'audiobook', dry_run: false }, busy: false, error: '', result: null
    };
    app.editionDialog = dialog;

    const creating = app.submitEditionCreate();
    app.closeEditionDialog();
    assert.equal(app.editionDialog, dialog);
    request.resolve({ response: { ok: true, status: 200 }, data: { success: true, data: {
        ...validCreateResult(),
        resync: { attempted: true, outcome: 'synced' }
    } } });
    await creating;

    assert.equal(dialog.busy, false);
    assert.equal(dialog.result.status, 'created');
    assert.ok(app.openSummary.addedEditionBookIds.has('li_1'));
    assert.equal(app.actionRefreshes, 1);
    assert.match(app.renderEditionDialog(dialog), /data-resync/);
});

test('session reset ignores a late forget 401 without expiring the new session', async t => {
    const harness = pendingDialogApp();
    t.after(harness.restore);
    const { app, content } = harness;
    const request = deferred();
    app.fetchJsonWithTimeout = () => request.promise;
    app.editionDialog = {
        mode: 'forget', profileId: 'private-profile', record: { book_id: 'li_2', hardcover_book_id: '42' },
        busy: false, error: '', result: null
    };
    const submitting = app.submitForget();
    const writesBeforeReset = harness.htmlWrites;
    app.resetSessionBoundState();
    request.resolve({ response: { ok: false, status: 401 }, data: { success: false } });
    await submitting;
    assert.equal(app.editionDialog, null);
    assert.equal(app.authExpiryCalls || 0, 0);
    assert.equal(harness.htmlWrites, writesBeforeReset);
    assert.equal(content.innerHTML, '');
});

function editionApp(overrides = {}) {
    global.window.sessionStorage?.clear?.();
    const app = createApp();
    app.statuses = { p1: { snapshot: { run_id: 'run-1', state: 'completed' } } };
    app.trackedRunIds = new Map();
    app.showEditionDialog = () => {};
    app.loadStatuses = async () => {};
    app.showToast = () => {};
    app.openSummary = {
        profileId: 'p1',
        runContext: { runId: 'run-1', state: 'completed', dryRun: false },
        records: new Map(),
        editionCapability: null,
        editionCapabilityLoaded: true
    };
    return Object.assign(app, overrides);
}

const needsReview = {
    book_id: 'li_1', outcome: 'needs_review', title: '<b>Dune</b>', format: 'audiobook',
    asin: 'B00ABC1234', hardcover_book_id: '42'
};

function validCreateResult(overrides = {}) {
    return {
        abs_item_id: 'li_1', reading_format: 'audiobook', status: 'created',
        hardcover_book_id: '42', hardcover_edition_id: '99', ...overrides
    };
}

test('create action requires an eligible needs-review record with an identifier and a permitted profile', () => {
    const app = editionApp();
    const ctx = app.openSummary.runContext;
    assert.equal(app.editionCreateIneligibleReason(needsReview, ctx), null);
    assert.match(app.editionCreateIneligibleReason({ ...needsReview, asin: '', isbn: '' }, ctx), /valid 10-character ASIN from Audiobookshelf/);
    assert.match(app.editionCreateIneligibleReason({ ...needsReview, asin: '', isbn: '9780306406157' }, ctx), /valid 10-character ASIN from Audiobookshelf/);
    assert.match(app.editionCreateIneligibleReason({ ...needsReview, asin: 'bad', isbn: '9780306406157' }, ctx), /valid 10-character ASIN from Audiobookshelf/);
    assert.equal(app.editionCreateIneligibleReason({ ...needsReview, format: 'ebook', asin: '', isbn: '9780306406157' }, ctx), null);
    assert.ok(app.editionCreateIneligibleReason({ ...needsReview, hardcover_book_id: '' }, ctx));
    assert.ok(app.editionCreateIneligibleReason({ ...needsReview, outcome: 'not_found' }, ctx));
    assert.ok(app.editionCreateIneligibleReason(needsReview, { ...ctx, state: 'running' }));
    assert.equal(app.editionCreateIneligibleReason(needsReview, { ...ctx, state: 'canceled' }), null);

    assert.match(app.renderEditionActions(needsReview), /data-edition-action="add"/);
    app.currentUser = { role: 'viewer' };
    app.authEnabled = true;
    app.isViewer = () => true;
    assert.equal(app.renderEditionActions(needsReview), '');
});

test('an ineligible needs-review record still shows a disabled Add edition button with the reason', () => {
    const app = editionApp();
    const html = app.renderEditionActions({ ...needsReview, hardcover_book_id: '' });
    assert.match(html, /data-edition-action="add"[^>]*disabled[^>]*title="No Hardcover book was matched for this item\."/);
    const isbnOnlyAudio = app.renderEditionActions({ ...needsReview, asin: '', isbn: '9780306406157' });
    assert.match(isbnOnlyAudio, /data-edition-action="add"[^>]*disabled[^>]*valid 10-character ASIN from Audiobookshelf/);
});

test('Add edition waits for the profile capability and disables only a confirmed format denial', () => {
    const app = editionApp();
    app.openSummary.editionCapabilityLoaded = false;
    let html = app.renderEditionActions(needsReview);
    assert.match(html, /disabled title="Checking whether this profile can add this edition\."/);

    app.openSummary.editionCapabilityLoaded = true;
    app.openSummary.editionCapability = {
        audiobook: { status: 'denied', can_attempt: false, reason: 'insufficient_scope' },
        ebook: { status: 'allowed', can_attempt: true }
    };
    html = app.renderEditionActions(needsReview);
    assert.match(html, /disabled title="This profile&#39;s Hardcover token does not have permission for this action\."/);

    const ebook = app.renderEditionActions({ ...needsReview, format: 'ebook' });
    assert.doesNotMatch(ebook, /disabled/);
});

test('unverified capability and failed probes leave Add edition available without a modal warning', async () => {
    const app = editionApp();
    app.openSummary.editionCapability = { audiobook: { status: 'unverified', can_attempt: true, warning: 'permission_unverified' } };
    assert.doesNotMatch(app.renderEditionActions(needsReview), /disabled/);
    assert.deepEqual(app.editionCapabilityGate(null, 'audiobook'), { blocked: false });

    global.document.getElementById = () => null;
    app.openSummary.editionCapabilityLoaded = false;
    app.fetchJsonWithTimeout = async () => { throw new Error('offline'); };
    await app.loadEditionCapability(app.openSummary);
    assert.equal(app.openSummary.editionCapabilityLoaded, true);
    assert.doesNotMatch(app.renderEditionActions(needsReview), /disabled/);
});

test('capability fetch refreshes the rendered Add edition button after pending, allowed, and denied results', async () => {
    const app = editionApp();
    const record = { ...needsReview };
    const summary = app.openSummary;
    summary.editionCapabilityLoaded = false;
    summary.records.set(record.book_id, record);

    const article = { dataset: { bookId: record.book_id } };
    const button = {
        disabled: true,
        title: 'Checking whether this profile can add this edition.',
        closest: () => article,
        removeAttribute(name) { if (name === 'title') delete this.title; }
    };
    const content = { querySelectorAll: selector => selector === '[data-edition-action="add"]' ? [button] : [] };
    const previousGetElementById = global.document.getElementById;
    global.document.getElementById = id => id === 'sync-summary-content' ? content : null;

    try {
        assert.match(app.renderEditionActions(record), /disabled title="Checking whether this profile can add this edition\."/);
        let resolveFetch;
        app.profileUrl = (profileId, path) => `/profiles/${profileId}${path}`;
        app.fetchJsonWithTimeout = () => new Promise(resolve => { resolveFetch = resolve; });
        const allowedLoad = app.loadEditionCapability(summary);
        assert.equal(button.disabled, true, 'the rendered action stays disabled while the probe is pending');
        resolveFetch({ response: { ok: true }, data: { success: true, data: { audiobook: { status: 'allowed', can_attempt: true } } } });
        await allowedLoad;
        assert.equal(button.disabled, false);
        assert.equal(button.title, undefined);

        summary.editionCapabilityLoaded = false;
        button.disabled = true;
        button.title = 'Checking whether this profile can add this edition.';
        const deniedLoad = app.loadEditionCapability(summary);
        assert.equal(button.disabled, true, 'a new probe keeps the action pending');
        resolveFetch({ response: { ok: true }, data: { success: true, data: { audiobook: { status: 'denied', can_attempt: false, reason: 'insufficient_scope' } } } });
        await deniedLoad;
        assert.equal(button.disabled, true);
        assert.match(button.title, /does not have permission/);
    } finally {
        global.document.getElementById = previousGetElementById;
    }
});

test('late capability denial keeps a pending request open for inspection without another create', async () => {
    const app = editionApp();
    const record = { ...needsReview };
    const summary = app.openSummary;
    summary.records.set(record.book_id, record);
    const pending = {
        submittedBody: { run_id: 'run-1', abs_item_id: 'li_1' }, outcome: 'transport_unknown',
        draft: { reading_format: 'audiobook' }, transportError: 'Check Hardcover before trying again.'
    };
    app.savePendingEditionRecovery({ ...pending, profileId: 'p1', runId: 'run-1', record });
    const button = { disabled: true, title: 'Denied', closest: () => ({ dataset: { bookId: 'li_1' } }), removeAttribute(name) { if (name === 'title') delete this.title; } };
    const previousGetElementById = global.document.getElementById;
    global.document.getElementById = id => id === 'sync-summary-content' ? { querySelectorAll: () => [button] } : null;
    try {
        summary.editionCapabilityLoaded = true;
        summary.editionCapability = { audiobook: { status: 'denied', can_attempt: false } };
        app.refreshEditionActionStates(summary);
        assert.equal(button.disabled, false);
        app.editionDialog = null;
        let requests = 0;
        app.fetchJsonWithTimeout = async () => { requests++; throw new Error('Recovery must not create'); };
        await app.openEditionDialog(record.book_id);
        assert.equal(requests, 0);
        assert.equal(app.editionDialog.outcome, 'transport_unknown');
        assert.doesNotMatch(app.renderEditionDialog(app.editionDialog), /confirm-create|retry-create/);
    } finally {
        global.document.getElementById = previousGetElementById;
    }
});

test('View Details refreshes denied permissions and ignores an older capability response', async t => {
    const app = editionApp();
    const summary = app.openSummary;
    summary.editionCapabilityLoaded = true;
    summary.editionCapability = {
        audiobook: { status: 'denied', can_attempt: false, reason: 'insufficient_scope' },
        dry_run: false
    };
    summary.expandedOutcomes = new Set();
    app.statuses = { p1: { profile_name: 'Profile One' } };

    const content = { innerHTML: '', querySelectorAll: () => [] };
    const tabs = { innerHTML: '' };
    const refreshButton = { disabled: false, textContent: 'Refresh permissions' };
    const previousDocument = global.document;
    global.document = {
        ...previousDocument,
        getElementById(id) { return id === 'sync-summary-content' ? content : id === 'sync-summary-tabs' ? tabs : null; },
        querySelector(selector) { return selector === '#sync-summary-content [data-edition-capability-refresh]' ? refreshButton : null; }
    };
    t.after(() => { global.document = previousDocument; });

    app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', outcome_counts: {}, book_outcomes: [] });
    assert.match(content.innerHTML, /data-edition-capability-refresh[^>]*>Refresh permissions/);

    const previousCapability = deferred();
    const refreshedCapability = deferred();
    let requests = 0;
    app.fetchJsonWithTimeout = (url, options) => {
        requests++;
        if (options?.method === 'POST') {
            assert.equal(url, '/api/profiles/p1/edition-capability/refresh');
            return refreshedCapability.promise;
        }
        assert.equal(url, '/api/profiles/p1/edition-capability');
        return previousCapability.promise;
    };
    const oldLoad = app.loadEditionCapability(summary);
    const refresh = app.refreshOpenEditionCapability();
    const duplicateRefresh = app.refreshOpenEditionCapability();
    assert.equal(requests, 2, 'a second click does not send another refresh request');
    assert.equal(refreshButton.disabled, true);
    assert.equal(refreshButton.textContent, 'Checking…');

    refreshedCapability.resolve({
        response: { ok: true, status: 200 },
        data: { success: true, data: { audiobook: { status: 'allowed', can_attempt: true }, dry_run: false } }
    });
    await refresh;
    previousCapability.resolve({
        response: { ok: true, status: 200 },
        data: { success: true, data: { audiobook: { status: 'denied', can_attempt: false, reason: 'insufficient_scope' }, dry_run: false } }
    });
    await Promise.all([oldLoad, duplicateRefresh]);

    assert.equal(summary.editionCapability.audiobook.status, 'allowed');
    assert.equal(summary.editionCapabilityLoaded, true);
    assert.equal(refreshButton.disabled, false);
    assert.equal(refreshButton.textContent, 'Refresh permissions');
});

test('View Details permission refresh is hidden from viewers and disabled during dry run', t => {
    const app = editionApp();
    const summary = app.openSummary;
    summary.editionCapabilityLoaded = true;
    summary.editionCapability = { audiobook: { status: 'unverified', can_attempt: true }, dry_run: false };
    summary.expandedOutcomes = new Set();
    app.statuses = { p1: { profile_name: 'Profile One' } };
    const content = { innerHTML: '', querySelectorAll: () => [] };
    const tabs = { innerHTML: '' };
    const previousDocument = global.document;
    global.document = { ...previousDocument, getElementById: id => id === 'sync-summary-content' ? content : id === 'sync-summary-tabs' ? tabs : null };
    t.after(() => { global.document = previousDocument; });

    app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', outcome_counts: {}, book_outcomes: [] });
    assert.match(content.innerHTML, /data-edition-capability-refresh[^>]*>Refresh permissions/);

    summary.editionCapability = { audiobook: { status: 'allowed', can_attempt: true }, dry_run: true };
    app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', outcome_counts: {}, book_outcomes: [] });
    assert.match(content.innerHTML, /data-edition-capability-refresh[^>]*disabled[^>]*>Refresh permissions/);

    app.authEnabled = true;
    app.currentUser = { role: 'viewer' };
    app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', outcome_counts: {}, book_outcomes: [] });
    assert.doesNotMatch(content.innerHTML, /data-edition-capability-refresh/);
});

test('a capability response from a previous profile cannot update the currently open summary', async () => {
    const app = editionApp();
    let resolveFetch;
    app.fetchJsonWithTimeout = () => new Promise(resolve => { resolveFetch = resolve; });
    const oldSummary = app.openSummary;
    oldSummary.editionCapabilityLoaded = false;
    const load = app.loadEditionCapability(oldSummary);
    const currentSummary = { ...oldSummary, profileId: 'p2', editionCapability: null, editionCapabilityLoaded: false };
    app.openSummary = currentSummary;
    resolveFetch({ response: { ok: true }, data: { success: true, data: { audiobook: { status: 'denied' } } } });
    await load;
    assert.equal(oldSummary.editionCapabilityLoaded, false);
    assert.equal(currentSummary.editionCapabilityLoaded, false);
    assert.equal(currentSummary.editionCapability, null);
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

test('the Add edition button renders inside the Hardcover candidate box, styled like Forget match', () => {
    const app = editionApp();
    const record = { ...needsReview, hardcover_title: 'Dune', hardcover_slug: 'dune' };
    const html = app.renderOutcomeRecord(record);
    const candidateIndex = html.indexOf('hardcover-candidate');
    const addButtonIndex = html.indexOf('data-edition-action="add"');
    assert.ok(candidateIndex !== -1 && addButtonIndex !== -1 && addButtonIndex > candidateIndex,
        'Add edition button should render inside the hardcover-candidate section');
    assert.match(html.slice(addButtonIndex - 80, addButtonIndex), /book-service-link edition-action-pill/);
});

test('capability gate blocks on known denial, permits unverified attempts, and passes when allowed', () => {
    const app = editionApp();
    const cap = {
        ebook: { status: 'allowed', can_attempt: true },
        audiobook: { status: 'denied', can_attempt: false, reason: 'no scope' }
    };
    assert.deepEqual(app.editionCapabilityGate(cap, 'audiobook'), { blocked: true, reason: 'no scope' });
    assert.deepEqual(app.editionCapabilityGate(cap, 'ebook'), { blocked: false });
    const unverified = app.editionCapabilityGate({ ebook: { status: 'unverified', can_attempt: true, warning: 'unverified!' } }, 'ebook');
    assert.deepEqual(unverified, { blocked: false });
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
    assert.doesNotMatch(html, /Audnexus details/);
    assert.match(html, /could not be confirmed/);
    assert.doesNotMatch(html, /Permission unverified|permission is unverified|cannot be checked/);
    assert.doesNotMatch(html, /name="audible_identifier"/);
    assert.doesNotMatch(html, /name="title"/);
    assert.doesNotMatch(html, /name="resync"/);

    const unavailable = app.renderEditionDialog({ ...dialog, draft: { ...dialog.draft, region_status: 'temporarily_unavailable' } });
    assert.match(unavailable, /temporarily unavailable/);
});

test('Audnexus details render only the independent Audnex preview fields', () => {
    const app = editionApp();
    const html = app.renderEditionDialog({
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false,
        capability: null,
        draft: {
            reading_format: 'audiobook', eligible: true, dry_run: false, region_status: 'confirmed', confirmed_region: 'uk',
            source_identifiers: { asin: 'B00ABC1234' }, audible_identifier_candidate: { asin: 'B00ABC1234' },
            metadata_preview: { title: 'ABS title', author: 'ABS author', narrator: 'ABS narrator', edition_information: 'Abridged' },
            audnexus_details: { title: 'Audnex title', author: 'Audnex author', narrator: 'Audnex narrator', release_date: '2023-08-09', format_type: 'Enhanced Audio' }
        }
    });
    const section = html.match(/<details class="edition-source-section edition-audnex-preview">([\s\S]*?)<\/details>/)?.[1] || '';
    assert.match(html, /Audnexus details <span class="edition-note">\(helps to verify the match\)<\/span>/);
    assert.match(section, /Audnex title/);
    assert.match(section, /Audnex author/);
    assert.match(section, /Audnex narrator/);
    assert.match(section, /2023-08-09/);
    assert.match(section, /Format type:<\/strong> Enhanced Audio/);
    assert.doesNotMatch(section, /ABS title|ABS author|ABS narrator|Abridged/);
});

test('an audiobook preview with no valid source ASIN cannot be confirmed, even if the draft was eligible', () => {
    const app = editionApp();
    const dialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false,
        capability: null,
        draft: {
            reading_format: 'audiobook', eligible: true, dry_run: false, region_status: 'unknown',
            source_identifiers: { asin: '', isbn: '9780306406157' },
            audible_identifier_candidate: { asin: '' }
        }
    };
    const html = app.renderEditionDialog(dialog);
    assert.match(html, /Audiobook edition creation requires a valid 10-character ASIN from Audiobookshelf/);
    assert.match(html, /data-edition-dialog="confirm-create"[^>]*disabled/);

    const validUnknownRegion = app.renderEditionDialog({
        ...dialog,
        draft: { ...dialog.draft, source_identifiers: { asin: 'B00ABC1234', isbn: '9780306406157' } }
    });
    assert.doesNotMatch(validUnknownRegion, /data-edition-dialog="confirm-create"[^>]*disabled/);
});

test('the Audible identifier is always plain text, never an editable field', () => {
    const app = editionApp();
    const base = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false,
        capability: null,
        draft: {
            reading_format: 'audiobook', eligible: true, dry_run: false,
            source_identifiers: { asin: 'B00ABC1234' }, audible_identifier_candidate: { asin: 'B00ABC1234' }
        }
    };
    const confirmed = app.renderEditionDialog({ ...base, draft: { ...base.draft, region_status: 'confirmed', confirmed_region: 'us' } });
    assert.doesNotMatch(confirmed, /name="audible_identifier"/);
    assert.match(confirmed, /B00ABC1234:us/);
    const unknown = app.renderEditionDialog({ ...base, draft: { ...base.draft, region_status: 'unknown' } });
    assert.doesNotMatch(unknown, /name="audible_identifier"/);
    assert.match(unknown, /retry region discovery during creation/);
    assert.match(unknown, /import proceeds only if a region is confirmed/);
});

test('candidate details omit Hardcover identifiers and show available Audiobookshelf metadata', () => {
    const app = editionApp();
    const candidate = {
        ...needsReview, hardcover_title: 'Dune', hardcover_author: 'Frank Herbert', hardcover_slug: 'dune',
        hardcover_asin: 'HC-ASIN', hardcover_isbn: 'HC-ISBN'
    };
    const statusHtml = app.renderOutcomeRecord(candidate);
    assert.doesNotMatch(statusHtml, /HC-ASIN|HC-ISBN/);
    assert.match(statusHtml, /Hardcover candidate/);
    for (const [isbn, series, seriesNumber, expectedSeries] of [
        ['', '', '', ''],
        ['   ', '', '', ''],
        ['9780441172719', 'Dune', '1', 'Dune #1'],
        ['', '<Dune>', '', '&lt;Dune&gt;']
    ]) {
        const record = { ...candidate, series, series_number: seriesNumber };
        const html = app.renderEditionDialog({
            mode: 'create', profileId: 'p1', runId: 'run-1', record, loading: false, busy: false,
            capability: null,
            draft: {
                reading_format: 'audiobook', eligible: true, dry_run: false, region_status: 'confirmed', confirmed_region: 'us',
                source_identifiers: { asin: 'B00ABC1234', isbn }, audible_identifier_candidate: { asin: 'B00ABC1234' }
            }
        });
        assert.match(html, /From Audiobookshelf/);
        assert.match(html, /Hardcover candidate/);
        assert.match(html, /Frank Herbert/);
        assert.doesNotMatch(html, /HC-ASIN|HC-ISBN/);
        if (isbn.trim()) assert.ok(html.includes(`<strong>ISBN:</strong> ${isbn}`));
        else assert.doesNotMatch(html, /<strong>ISBN:/);
        if (expectedSeries) assert.ok(html.includes(`<strong>Series:</strong> ${expectedSeries}`));
        else assert.doesNotMatch(html, /<strong>Series:/);
    }
});

test('capability denial codes are translated to plain-language text', () => {
    const app = editionApp();
    assert.match(
        app.editionCapabilityGate({ audiobook: { status: 'denied', can_attempt: false, reason: 'hardcover_token_missing' } }, 'audiobook').reason,
        /no Hardcover token configured/
    );
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
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '0-306-40615-2', original: '' },
        isbn_13: { value: '', original: '' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_10: '0-306-40615-2' });
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '', original: '' },
        isbn_13: { value: '9780306406157', original: '' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_13: '9780306406157' });
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '', original: '0306406152' },
        isbn_13: { value: '9780306406157', original: '9780306406157' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_10: '', isbn_13: '9780306406157' });
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '0306406152', original: '0306406152' },
        isbn_13: { value: '', original: '9780306406157' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_13: '', isbn_10: '0306406152' });
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '', original: '0306406152' },
        isbn_13: { value: '', original: '9780306406157' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_10: '', isbn_13: '' });
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '0306406152', original: '' },
        isbn_13: { value: '9780306406157', original: '' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_10: '0306406152', isbn_13: '9780306406157' });
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '0306406152', original: '' },
        isbn_13: { value: '9780000000002', original: '' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_10: '0306406152', isbn_13: '9780000000002' });
    assert.deepEqual(app.buildEditionCreateBody(dialog, {
        isbn_10: { value: '0306406152', original: '0306406152' },
        isbn_13: { value: '9780000000002', original: '9780306406157' }
    }, false), { run_id: 'run-1', abs_item_id: 'li_9', isbn_13: '9780000000002' });
    assert.equal(app.buildEditionCreateBody(dialog, fields, true).resync, true);

    const confirmedAudio = {
        runId: 'run-1', record: { book_id: 'li_9' },
        draft: { reading_format: 'audiobook', region_status: 'confirmed', confirmed_region: 'uk', audible_identifier_candidate: { asin: 'B00ABC1234' } }
    };
    assert.deepEqual(app.buildEditionCreateBody(confirmedAudio, {}, false), {
        run_id: 'run-1', abs_item_id: 'li_9', audible_identifier: 'B00ABC1234:uk'
    });

    const unconfirmedAudio = { runId: 'run-1', record: { book_id: 'li_9' }, draft: { reading_format: 'audiobook', region_status: 'unknown' } };
    assert.deepEqual(app.buildEditionCreateBody(unconfirmedAudio, {}, false), {
        run_id: 'run-1', abs_item_id: 'li_9'
    });
});

test('legacy unclassified server errors remain visible and do not trigger a second import', async () => {
    const app = editionApp();
    const dialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false, error: '', result: null,
        draft: { reading_format: 'ebook', dry_run: false, eligible: true, warnings: [], source_identifiers: {}, ebook_candidate: {
            title: 'Original', subtitle: '', asin: '', isbn_10: 'old10', isbn_13: 'old13', release_date: '', edition_format: 'Ebook'
        } }
    };
    app.editionDialog = dialog;
    app.showEditionDialog = SyncProfileApp.prototype.showEditionDialog;
    const originalDocument = global.document;
    const modal = { style: {} };
    const content = { fields: {}, body: null,
        querySelector() { return this.body; },
        set innerHTML(html) {
        this.html = html;
        this.body = { scrollTop: 0 };
        const decode = value => value.replace(/&quot;/g, '"').replace(/&#39;/g, "'").replace(/&lt;/g, '<').replace(/&gt;/g, '>').replace(/&amp;/g, '&');
        this.fields = Object.fromEntries([...html.matchAll(/<input\b[^>]*>/g)].map(([input]) => {
            const name = input.match(/\bname="([^"]*)"/)?.[1];
            const value = input.match(/\bvalue="([^"]*)"/)?.[1] || '';
            const original = input.match(/\bdata-original="([^"]*)"/)?.[1] || '';
            return [name, { name, value: decode(value), dataset: { original: decode(original) } }];
        }));
    } };
    global.document = {
        getElementById(id) { return id === 'edition-modal' ? modal : id === 'edition-modal-content' ? content : null; },
        createElement(...args) { return originalDocument.createElement(...args); },
        querySelector() { return { querySelectorAll() { return Object.values(content.fields); } }; }
    };
    try {
        app.showEditionDialog();
        content.body.scrollTop = 500;
        assert.ok(content.fields.title);
        content.fields.title.value = '<New & title>';
        content.fields.isbn_10.value = '';
        content.fields.isbn_13.value = '9780000000002';
        const requests = [];
        app.fetchJsonWithTimeout = async (_url, options) => {
            requests.push(JSON.parse(options.body));
            return { response: { ok: false, status: 500 }, data: { success: false, error: 'temporary failure' } };
        };
        await app.submitEditionCreate();
        assert.equal(content.body.scrollTop, 500);
        assert.match(content.html, /temporary failure/);
        assert.match(content.html, /HTTP 500/);
        assert.doesNotMatch(content.html, /data-edition-dialog="confirm-create"/);
        assert.deepEqual(requests[0], { run_id: 'run-1', abs_item_id: 'li_1', title: '<New & title>', isbn_10: '', isbn_13: '9780000000002', resync: true });
        await app.submitEditionCreate();
        assert.equal(content.body.scrollTop, 500);
        assert.equal(requests.length, 1);
    } finally {
        global.document = originalDocument;
    }
});

test('submitting an edition create uses a timeout long enough for the server\'s own budget', async () => {
    const app = editionApp();
    let capturedOptions;
    app.fetchJsonWithTimeout = async (_url, options) => {
        capturedOptions = options;
        return { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } };
    };
    app.readEditionFormFields = () => ({});
    app.editionDialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, busy: false, error: '', result: null,
        draft: { reading_format: 'audiobook', region_status: 'confirmed', confirmed_region: 'us', audible_identifier_candidate: { asin: 'B00ABC1234' }, dry_run: false }
    };
    await app.submitEditionCreate();
    assert.ok(capturedOptions.timeoutMs >= 65000, 'client timeout must cover the 65s server-side editionCreateRequestTimeout');
});

function stubDialog(app, status, payload) {
    app.fetchJsonWithTimeout = async () => ({ response: { ok: status < 300, status, headers: { get: () => null } }, data: payload });
    app.readEditionFormFields = () => ({});
    app.editionDialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, busy: false, error: '', result: null,
        draft: { reading_format: 'audiobook', region_status: 'confirmed', confirmed_region: 'us', audible_identifier_candidate: { asin: 'B00ABC1234' }, dry_run: false }
    };
    return app.editionDialog;
}

test('successful create updates the book action immediately and shows a separate resync failure', async t => {
    const app = editionApp();
    app.openSummary.records.set(String(needsReview.book_id), needsReview);
    const button = { closest() { return { dataset: { bookId: needsReview.book_id } }; }, outerHTML: '' };
    const previousDocument = global.document;
    global.document = { ...previousDocument, getElementById(id) {
        return id === 'sync-summary-content' ? { querySelectorAll() { return [button]; } } : null;
    } };
    t.after(() => { global.document = previousDocument; });
    const dialog = stubDialog(app, 200, { success: true, data: {
        ...validCreateResult(),
        resync: { attempted: true, error: 'hardcover down' }
    } });
    await app.submitEditionCreate();
    assert.match(button.outerHTML, /Hardcover Edition Added/);
    assert.match(app.renderEditionActions(needsReview), /Hardcover Edition Added/);
    assert.doesNotMatch(app.renderEditionActions(needsReview), /data-edition-action="add"/);
    assert.equal(needsReview.outcome, 'needs_review');
    const html = app.renderEditionDialog(dialog);
    assert.match(html, /edition was created/);
    assert.match(html, /resync failed: hardcover down/);
    const current = app.renderCreateResult({
        status: 'created',
        resync: { attempted: true, outcome: 'already_current', reason: 'Hardcover finished status already current' }
    });
    assert.match(current, /data-resync>Resync finished \(Hardcover finished status already current\)\.<\/div>/);
});

test('malformed create success envelopes stay unknown and never mark the item added', async () => {
    for (const malformedData of [undefined, 'created', [], {},
        validCreateResult({ abs_item_id: 'another-item' }),
        validCreateResult({ hardcover_book_id: '43' }),
        validCreateResult({ reading_format: 'ebook' }),
        validCreateResult({ status: 'pending' }),
        validCreateResult({ hardcover_edition_id: '0' })]) {
        const app = editionApp();
        const dialog = stubDialog(app, 200, { success: true, data: malformedData });
        await app.submitEditionCreate();
        assert.equal(dialog.outcome, 'transport_unknown');
        assert.equal(dialog.result, null);
        assert.doesNotMatch(app.renderEditionDialog(dialog), /data-edition-dialog="confirm-create"/);
        assert.equal(app.openSummary.addedEditionBookIds?.has('li_1') || false, false);
    }
    const app = editionApp();
    const dialog = stubDialog(app, 200, { success: 1, data: validCreateResult() });
    await app.submitEditionCreate();
    assert.equal(dialog.outcome, 'transport_unknown');
    assert.equal(dialog.result, null);
});

test('valid loaded, reused, and existing create results are accepted', async () => {
    for (const status of ['loaded', 'reused', 'existing']) {
        const app = editionApp();
        const dialog = stubDialog(app, 200, { success: true, data: validCreateResult({ status }) });
        await app.submitEditionCreate();
        assert.equal(dialog.outcome, '');
        assert.equal(dialog.result.status, status);
        assert.ok(app.openSummary.addedEditionBookIds.has('li_1'));
    }
});

test('malformed check-import success stays pending and does not mark the item added', async () => {
    for (const malformedData of [undefined, 'created', [], {},
        validCreateResult({ abs_item_id: 'another-item' }),
        validCreateResult({ hardcover_book_id: '43' }),
        validCreateResult({ reading_format: 'ebook' }),
        validCreateResult({ status: 'pending' }),
        validCreateResult({ hardcover_edition_id: '-1' })]) {
        const app = editionApp();
        const dialog = stubDialog(app, 503, { success: false, outcome: 'unconfirmed', data: {
            audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
        } });
        await app.submitEditionCreate();
        const recoveryBefore = dialog.recovery;
        app.fetchJsonWithTimeout = async () => ({ response: { ok: true, status: 200 }, data: { success: true, data: malformedData } });
        await app.checkEditionImport();

        assert.equal(dialog.outcome, 'unconfirmed');
        assert.equal(dialog.result, null);
        assert.equal(dialog.recovery, recoveryBefore);
        assert.match(dialog.checkError, /Could not check the import status/);
        assert.ok(app.loadPendingEditionRecovery('p1', 'run-1', 'li_1'));
        assert.equal(app.openSummary.addedEditionBookIds?.has('li_1') || false, false);
    }
});

test('create result explains resync outcomes that did not apply read status', () => {
    const app = editionApp();
    const cases = [
        ['skipped', 'book was skipped'],
        ['needs_review', 'match still needs review'],
        ['not_found', 'no matching Hardcover edition was found'],
        ['would_sync', 'this was a dry run']
    ];
    for (const [outcome, explanation] of cases) {
        const html = app.renderCreateResult({ status: 'created', resync: {
            attempted: true, outcome, reason: 'Reason <from API>'
        } });
        assert.match(html, /The Hardcover edition was created/);
        assert.match(html, /Read status was not synced/);
        assert.ok(html.includes(explanation));
        assert.match(html, /Reason &lt;from API&gt;/);
        assert.doesNotMatch(html, /Resync finished/);
    }
});

test('successful create does not mark a different run as resolved', async () => {
    const app = editionApp();
    stubDialog(app, 200, { success: true, data: validCreateResult() });
    app.openSummary.runContext.runId = 'run-2';
    await app.submitEditionCreate();
    assert.match(app.renderEditionActions(needsReview), /data-edition-action="add"/);
    assert.doesNotMatch(app.renderEditionActions(needsReview), /Hardcover Edition Added/);
});

test('create result describes loaded regional imports and reused ebook editions as existing', () => {
    const app = editionApp();
    for (const status of ['loaded', 'reused', 'existing']) {
        assert.match(app.renderCreateResult({ status }), /An existing Hardcover edition was reused/);
    }
    assert.match(app.renderCreateResult({ status: 'created' }), /The Hardcover edition was created/);
});

test('create failures surface permission denial and stale 409 clearly', async () => {
    const app = editionApp();
    let refreshed = 0;
    app.loadStatuses = async () => { refreshed++; };
    let dialog = stubDialog(app, 403, { success: false, error: 'Hardcover token is missing catalogue write permission' });
    await app.submitEditionCreate();
    assert.equal(dialog.outcome, 'transport_unknown');
    assert.match(app.renderEditionDialog(dialog), /Hardcover token is missing catalogue write permission/);
    assert.equal(dialog.result, null);

    dialog = stubDialog(app, 409, { success: false, error: 'sync run no longer contains a usable needs-review source record' });
    await app.submitEditionCreate();
    assert.equal(dialog.outcome, 'transport_unknown');
    assert.match(app.renderEditionDialog(dialog), /no longer contains.*record may be stale, or a sync started after this page loaded; refresh and try again/);
    assert.match(app.renderEditionActions(needsReview), /data-edition-action="add"/);
    assert.equal(refreshed, 1);
});

test('known pre-write authorization denials clear the user marker and permit a later create', async () => {
    const cases = [
        [403, { success: false, error: 'Insufficient permissions' }],
        [404, { success: false, error: 'Sync profile not found' }],
        [403, { error: { code: 'insufficient_permissions', message: 'Insufficient permissions' } }]
    ];
    for (const [status, payload] of cases) {
        const app = editionApp({ authEnabled: true, currentUser: { id: 'user-7' } });
        const dialog = stubDialog(app, status, payload);
        const key = app.pendingEditionRecoveryKey('p1', 'run-1', 'li_1');
        let creates = 0;
        app.fetchJsonWithTimeout = async () => {
            creates++;
            return creates === 1
                ? { response: { ok: false, status }, data: payload }
                : { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } };
        };
        await app.submitEditionCreate();
        assert.equal(dialog.outcome, 'not_submitted');
        assert.equal(window.sessionStorage.getItem(key), null);
        assert.match(dialog.error, /Permission denied|not found/);
        const reloaded = Object.assign(createApp(), { authEnabled: true, currentUser: { id: 'user-7' } });
        assert.equal(reloaded.loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
        await app.submitEditionCreate();
        assert.equal(creates, 2);
        assert.ok(dialog.result);
    }
});

test('recognized auth expiry payloads clear the user marker before resetting the signed-in user', async () => {
    for (const payload of [
        { success: false, error: 'Authentication required' },
        { error: { code: 'authentication_required', message: 'No authentication token provided' } },
        { error: { code: 'authentication_required', message: 'Invalid or expired token' } }
    ]) {
        const app = editionApp({ authEnabled: true, currentUser: { id: 'user-8' } });
        const dialog = stubDialog(app, 401, payload);
        const key = app.pendingEditionRecoveryKey('p1', 'run-1', 'li_1');
        app.closeEditionDialog = () => { app.editionDialog = null; };
        app.abortSessionMutations = () => {};
        app.closeEditModal = () => {};
        app.clearOpenSummary = () => { app.openSummary = null; };
        app.resetProfileRetry = () => {};
        app.renderProfiles = () => {};
        app.renderStatuses = () => {};
        app.stopAutoRefresh = () => {};
        app.showToast = () => {};
        app.redirectToLogin = () => { app.redirectCalls = (app.redirectCalls || 0) + 1; };
        app.terminalErrorCache = new Map();
        app.terminalErrorRetries = new Map();
        app.terminalErrorRequests = new Map();
        app.trackedRunIds = new Map();
        app.statusRefreshWaiters = [];
        await app.submitEditionCreate();
        assert.equal(window.sessionStorage.getItem(key), null);
        assert.equal(app.currentUser, null);
        assert.equal(app.redirectCalls, 1);
        assert.equal(app.editionDialog, null);
        assert.equal(dialog.outcome, '');
    }
});

test('unidentified authorization denials remain unknown and cannot be retried in the dialog', async () => {
    for (const [status, payload] of [
        [403, { success: false, error: 'proxy denied' }],
        [404, { success: false, error: 'not found' }],
        [401, { success: false }]
    ]) {
        const app = editionApp();
        const dialog = stubDialog(app, status, payload);
        const key = app.pendingEditionRecoveryKey('p1', 'run-1', 'li_1');
        let creates = 0;
        app.fetchJsonWithTimeout = async () => { creates++; return { response: { ok: false, status }, data: payload }; };
        app.closeEditionDialog = () => { app.editionDialog = null; };
        app.handleAuthExpiry = () => {};
        await app.submitEditionCreate();
        assert.notEqual(window.sessionStorage.getItem(key), null);
        if (status !== 401) {
            assert.equal(dialog.outcome, 'transport_unknown');
            await app.submitEditionCreate();
            assert.equal(creates, 1);
        }
    }
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

test('draft preview 429 countdown preserves edits in the loaded ebook form', async t => {
    const app = editionApp();
    const dialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: { ...needsReview, format: 'ebook' },
        busy: false, loading: false, error: '', draft: {
            reading_format: 'ebook', eligible: true, ebook_candidate: {
                title: 'Original title', isbn_13: '9780000000001'
            }
        }, capability: null, retryAt: 0, fieldValues: null
    };
    app.editionDialog = dialog;
    app.profileUrl = () => '/profile';
    app.fetchJsonWithTimeout = async url => url.includes('edition-capability')
        ? { response: { ok: true, status: 200 }, data: { success: true, data: {} } }
        : { response: { ok: false, status: 429, headers: { get: () => '2' } }, data: { success: false, error: 'busy' } };

    let now = 100000;
    const originalNow = Date.now;
    const originalSetInterval = global.setInterval;
    const originalClearInterval = global.clearInterval;
    let tick;
    let fields = [];
    const retryButton = { textContent: 'Refresh preview', disabled: false };
    const content = {
        set innerHTML(_html) {
            fields = [
                { name: 'title', value: 'Original title', dataset: { original: 'Original title' } },
                { name: 'isbn_13', value: '9780000000001', dataset: { original: '9780000000001' } }
            ];
        },
        querySelector(selector) { return selector === '[data-edition-dialog="retry"]' ? retryButton : null; }
    };
    const previousDocument = global.document;
    global.document = {
        getElementById(id) { return id === 'edition-modal-content' ? content : id === 'edition-modal' ? { style: {} } : null; },
        createElement(...args) { return previousDocument.createElement(...args); },
        querySelector(selector) {
            return selector === '#edition-modal-content .edition-form'
                ? { querySelectorAll: () => fields }
                : null;
        }
    };
    Date.now = () => now;
    global.setInterval = callback => { tick = callback; return 1; };
    global.clearInterval = () => {};
    t.after(() => {
        global.document = previousDocument;
        Date.now = originalNow;
        global.setInterval = originalSetInterval;
        global.clearInterval = originalClearInterval;
    });

    app.showEditionDialog = SyncProfileApp.prototype.showEditionDialog.bind(app);
    await app.loadEditionDraft();
    fields.find(field => field.name === 'title').value = 'Corrected title';
    fields.find(field => field.name === 'isbn_13').value = '9780000000002';

    now += 1000;
    tick();
    assert.equal(retryButton.textContent, 'Retry in 1s');
    assert.deepEqual(app.buildEditionCreateBody(dialog, app.readEditionFormFields(), false), {
        run_id: 'run-1', abs_item_id: String(needsReview.book_id),
        title: 'Corrected title', isbn_13: '9780000000002'
    });

    now += 1000;
    tick();
    assert.equal(retryButton.textContent, 'Refresh preview');
    assert.equal(retryButton.disabled, false);
    assert.equal(dialog.timer, null);
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

test('forget uses fresh capability, waits while checking, and lets the server decide when unavailable', async () => {
    const record = { book_id: 'li_2', outcome: 'synced', title: 'T', hardcover_book_id: '7', edition_id: '9' };
    const makeApp = runDryRun => {
        const app = editionApp({ isMatchedRecord: () => true, isViewer: () => false, profileIsSyncing: () => false });
        app.openSummary.runContext.dryRun = runDryRun;
        app.openSummary.records.set(String(record.book_id), record);
        return app;
    };

    for (const runDryRun of [true, false]) {
        const app = makeApp(runDryRun);
        const capability = { dry_run: !runDryRun };
        app.fetchJsonWithTimeout = async () => ({ response: { ok: true, status: 200 }, data: { success: true, data: capability } });
        await app.openForgetDialog(record.book_id);
        assert.equal(app.editionDialog.loading, false);
        assert.equal(app.renderEditionDialog(app.editionDialog).includes('data-edition-dialog="confirm-forget" disabled'), capability.dry_run);
    }

    const pendingApp = makeApp(false);
    const capabilityRequest = deferred();
    let requests = 0;
    pendingApp.fetchJsonWithTimeout = () => { requests++; return capabilityRequest.promise; };
    const opening = pendingApp.openForgetDialog(record.book_id);
    assert.match(pendingApp.renderEditionDialog(pendingApp.editionDialog), /Checking this profile/);
    assert.match(pendingApp.renderEditionDialog(pendingApp.editionDialog), /confirm-forget" disabled/);
    await pendingApp.submitForget();
    assert.equal(requests, 1);
    capabilityRequest.resolve({ response: { ok: true, status: 200 }, data: { success: true, data: { dry_run: false } } });
    await opening;
    assert.doesNotMatch(pendingApp.renderEditionDialog(pendingApp.editionDialog), /confirm-forget"[^>]*disabled/);

    const unknownApp = makeApp(true);
    unknownApp.fetchJsonWithTimeout = async (_url, options) => options?.method === 'DELETE'
        ? { response: { ok: true, status: 200 }, data: { success: true, data: {
            association_removed: false, dry_run: true,
            previous_resolution: { hardcover_book_id: '7', hardcover_edition_id: '9' }
        } } }
        : Promise.reject(new Error('capability unavailable'));
    await unknownApp.openForgetDialog(record.book_id);
    assert.doesNotMatch(unknownApp.renderEditionDialog(unknownApp.editionDialog), /confirm-forget"[^>]*disabled/);
    await unknownApp.submitForget();
    assert.match(unknownApp.renderEditionDialog(unknownApp.editionDialog), /saved match was retained/);
});

test('edition dialog closes on Escape except while creating, and restores focus to its opener', () => {
    const originalDocument = global.document;
    const opener = { isConnected: true, focused: 0, focus() { this.focused += 1; } };
    const modal = { style: { display: 'block' } };
    const content = { replaceChildren() {} };
    global.document = {
        ...originalDocument,
        activeElement: null,
        getElementById(id) { return id === 'edition-modal' ? modal : id === 'edition-modal-content' ? content : null; }
    };
    try {
        const app = createApp();
        app.editionDialog = { mode: 'create', busy: true };
        app.editionDialogOpener = opener;
        app.handleEditionDialogKeydown({ key: 'Escape' });
        assert.ok(app.editionDialog, 'Escape must not close a create request in flight');
        assert.equal(opener.focused, 0);

        app.editionDialog.busy = false;
        app.handleEditionDialogKeydown({ key: 'Escape' });
        assert.equal(app.editionDialog, null);
        assert.equal(modal.style.display, 'none');
        assert.equal(opener.focused, 1);

        // A forced close (session reset) and a detached opener never take focus.
        app.editionDialog = { mode: 'forget' };
        app.editionDialogOpener = opener;
        app.closeEditionDialog(true);
        app.editionDialog = { mode: 'forget' };
        app.editionDialogOpener = { ...opener, isConnected: false };
        app.closeEditionDialog();
        assert.equal(opener.focused, 1);
    } finally {
        global.document = originalDocument;
    }
});

test('edition dialog keeps Tab focus inside the modal', () => {
    const originalDocument = global.document;
    const makeButton = () => ({ tabIndex: 0, focused: false, focus() { global.document.activeElement = this; } });
    const [first, last] = [makeButton(), makeButton()];
    const content = { querySelectorAll() { return [first, last]; } };
    global.document = { ...originalDocument, activeElement: last, getElementById(id) { return id === 'edition-modal-content' ? content : null; } };
    try {
        const app = createApp();
        app.editionDialog = { mode: 'forget' };
        app.handleEditionDialogKeydown({ key: 'Tab', shiftKey: false, preventDefault() {} });
        assert.equal(global.document.activeElement, first);
        app.handleEditionDialogKeydown({ key: 'Tab', shiftKey: true, preventDefault() {} });
        assert.equal(global.document.activeElement, last);
        const outside = {};
        global.document.activeElement = outside;
        app.handleEditionDialogKeydown({ key: 'Tab', shiftKey: false, preventDefault() {} });
        assert.equal(global.document.activeElement, first);
    } finally {
        global.document = originalDocument;
    }
});

test('edition dialog allows Tab through recovery details and links in DOM order', () => {
    const originalDocument = global.document;
    const control = (tagName, attributes = {}, options = {}) => ({
        tagName, attributes, tabIndex: 0, ...options,
        focus() { global.document.activeElement = this; }
    });
    const headerClose = control('button');
    const details = control('summary');
    const checkImport = control('button');
    const hardcoverLink = control('a', { href: 'https://hardcover.app/books/test' });
    const disabledCheck = control('button', {}, { disabled: true });
    const hiddenControl = control('input', {}, { hidden: true });
    const programmaticControl = control('div', { tabindex: '-1' }, { tabIndex: -1 });
    const customControl = control('div', { tabindex: '0' });
    const footerClose = control('button');
    const controls = [headerClose, details, checkImport, hardcoverLink, disabledCheck, hiddenControl, programmaticControl, customControl, footerClose];
    const content = {
        querySelectorAll(selector) {
            // Model tag and attribute selection while retaining document order.
            return controls.filter(element => selector.split(',').some(part => {
                const tag = part.trim().match(/^[a-z]+/i)?.[0];
                const attributes = [...part.matchAll(/\[([a-z]+)\]/gi)].map(match => match[1]);
                return (!tag || element.tagName === tag) && attributes.every(name => name in element.attributes);
            }));
        }
    };
    global.document = { ...originalDocument, getElementById(id) { return id === 'edition-modal-content' ? content : null; } };
    try {
        const app = createApp();
        app.editionDialog = { mode: 'create', outcome: 'unconfirmed' };
        const expected = [headerClose, details, checkImport, hardcoverLink, customControl, footerClose];
        assert.deepEqual(app.editionDialogFocusables(), expected);
        for (const active of expected.slice(1, -1)) {
            global.document.activeElement = active;
            for (const shiftKey of [false, true]) {
                let prevented = false;
                app.handleEditionDialogKeydown({ key: 'Tab', shiftKey, preventDefault() { prevented = true; } });
                assert.equal(prevented, false, `${active.tagName} should advance in native Tab order`);
                assert.equal(global.document.activeElement, active);
            }
        }
        global.document.activeElement = footerClose;
        app.handleEditionDialogKeydown({ key: 'Tab', preventDefault() {} });
        assert.equal(global.document.activeElement, headerClose);
        app.handleEditionDialogKeydown({ key: 'Tab', shiftKey: true, preventDefault() {} });
        assert.equal(global.document.activeElement, footerClose);
    } finally {
        global.document = originalDocument;
    }
});


test('added edition labels survive a page reload and remain scoped to user, profile, and run', async t => {
    const previousStorage = window.localStorage;
    const previousDocument = global.document;
    const stored = new Map();
    window.localStorage = {
        getItem(key) { return stored.get(key) ?? null; },
        setItem(key, value) { stored.set(key, value); }
    };
    global.document = { ...previousDocument, getElementById() { return { scrollIntoView() {} }; } };
    t.after(() => { window.localStorage = previousStorage; global.document = previousDocument; });

    const app = editionApp({ authEnabled: true, currentUser: { id: 'user-1' } });
    stubDialog(app, 200, { success: true, data: { ...validCreateResult(), resync: { attempted: true, error: 'offline' } } });
    await app.submitEditionCreate();
    app.editionDialog = null;
    app.saveAddedEditionBookId('p1', 'run-1', 'li_2');

    async function reload(userId, profileId, runId) {
        const refreshed = editionApp({ authEnabled: true, currentUser: { id: userId }, openSummary: null });
        refreshed.statuses = { [profileId]: { snapshot: { run_id: runId, state: 'completed' } } };
        refreshed.renderStatuses = () => {};
        refreshed.renderDetailsState = () => {};
        refreshed.loadEditionCapability = () => {};
        refreshed.fetchAndRenderDetails = async () => {};
        await refreshed.showSyncSummary(profileId);
        return refreshed;
    }
    const refreshed = await reload('user-1', 'p1', 'run-1');
    assert.match(refreshed.renderEditionActions(needsReview), /Hardcover Edition Added/);
    assert.match(refreshed.renderEditionActions({ ...needsReview, book_id: 'li_2' }), /Hardcover Edition Added/);
    assert.match(refreshed.renderEditionActions({ ...needsReview, book_id: 'li_3' }), /data-edition-action="add"/);
    for (const scope of [['user-2', 'p1', 'run-1'], ['user-1', 'p2', 'run-1'], ['user-1', 'p1', 'run-2']]) {
        const other = await reload(...scope);
        assert.doesNotMatch(other.renderEditionActions(needsReview), /Hardcover Edition Added/);
    }
});

test('failed creates and unavailable or malformed browser storage do not restore added labels', async t => {
    const previousStorage = window.localStorage;
    t.after(() => { window.localStorage = previousStorage; });
    let writes = 0;
    window.localStorage = { getItem() { return '{invalid'; }, setItem() { writes++; } };
    const app = editionApp();
    assert.equal(app.loadAddedEditionBookIds('p1', 'run-1').size, 0);
    stubDialog(app, 500, { success: false, error: 'failed' });
    await app.submitEditionCreate();
    assert.equal(writes, 0);
    assert.doesNotMatch(app.renderEditionActions(needsReview), /Hardcover Edition Added/);

    window.localStorage = {
        getItem() { throw new Error('storage blocked'); },
        setItem() { throw new Error('storage blocked'); }
    };
    stubDialog(app, 200, { success: true, data: validCreateResult() });
    await app.submitEditionCreate();
    assert.match(app.renderEditionActions(needsReview), /Hardcover Edition Added/);
    assert.equal(app.editionDialog.error, '');
});

test('create is not sent unless the pending marker is durably saved in session storage', async () => {
    const originalStorage = window.sessionStorage;
    const partiallyWritten = new Map();
    const blockedCases = [
        { name: 'missing', storage: null },
        { name: 'throws', storage: { getItem() { throw new Error('blocked'); }, setItem() { throw new Error('blocked'); } } },
        { name: 'silently ignores writes', storage: { getItem() { return null; }, setItem() {} } },
        { name: 'readback fails after writing', storage: {
            clear() { partiallyWritten.clear(); },
            getItem() { throw new Error('readback blocked'); },
            setItem(key, value) { partiallyWritten.set(key, value); },
            removeItem(key) { partiallyWritten.delete(key); }
        }, verify() {
            const reloaded = createApp();
            assert.equal(reloaded.loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
            assert.equal(partiallyWritten.size, 0);
        } }
    ];
    try {
        for (const blocked of blockedCases) {
            window.sessionStorage = blocked.storage;
            const app = editionApp();
            const dialog = stubDialog(app, 200, {});
            app.readEditionFormFields = () => {
                dialog.fieldValues = { title: 'Edited title' };
                return { title: { value: 'Edited title', original: 'Original title' } };
            };
            let creates = 0;
            app.fetchJsonWithTimeout = async () => { creates++; throw new Error('must not send'); };
            await app.submitEditionCreate();
            assert.equal(creates, 0, blocked.name);
            assert.equal(dialog.outcome, 'not_submitted');
            assert.equal(dialog.retryCreate, true);
            assert.deepEqual(dialog.fieldValues, { title: 'Edited title' });
            assert.match(dialog.error, /nothing was sent/);
            assert.equal(app.pendingEditionRecoveries?.size || 0, 0);
            blocked.verify?.();
        }

        window.sessionStorage = originalStorage;
        originalStorage.clear();
        const app = editionApp();
        const dialog = stubDialog(app, 200, {});
        const request = deferred();
        let creates = 0;
        app.fetchJsonWithTimeout = () => { creates++; return request.promise; };
        const submitting = app.submitEditionCreate();
        assert.equal(creates, 1);
        const refreshed = createApp();
        assert.equal(refreshed.loadPendingEditionRecovery('p1', 'run-1', 'li_1').outcome, 'transport_unknown');
        request.resolve({ response: { ok: false, status: 503 }, data: { success: false, outcome: 'not_submitted' } });
        await submitting;
        assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
        assert.equal(dialog.outcome, 'not_submitted');
    } finally {
        window.sessionStorage = originalStorage;
        originalStorage.clear();
    }
});

test('reload during an in-flight create blocks another import until a definite response', async () => {
    for (const terminal of [
        { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } },
        { response: { ok: false, status: 503 }, data: { success: false, outcome: 'not_submitted', error: 'No import was sent' } }
    ]) {
        const app = editionApp();
        stubDialog(app, 200, {});
        const request = deferred();
        let creates = 0;
        app.fetchJsonWithTimeout = () => { creates++; return request.promise; };
        const submitting = app.submitEditionCreate();
        const refreshed = Object.assign(createApp(), {
            openSummary: { ...app.openSummary, records: new Map([[needsReview.book_id, needsReview]]) },
            statuses: app.statuses, showEditionDialog() {},
            fetchJsonWithTimeout() { creates++; throw new Error('Unexpected second create'); }
        });
        await refreshed.openEditionDialog(needsReview.book_id);
        assert.equal(refreshed.editionDialog.outcome, 'transport_unknown');
        assert.deepEqual(refreshed.editionDialog.submittedBody, {
            run_id: 'run-1', abs_item_id: 'li_1', audible_identifier: 'B00ABC1234:us', resync: true
        });
        const html = refreshed.renderEditionDialog(refreshed.editionDialog);
        assert.match(html, /import result is unknown/);
        assert.match(html, /Open Hardcover/);
        assert.doesNotMatch(html, /confirm-create|retry-create|check-import/);
        await refreshed.submitEditionCreate();
        assert.equal(creates, 1);
        request.resolve(terminal);
        await submitting;
        assert.equal(createApp().loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
    }
});

test('ambiguous create shows a safe recovery action and preserves submitted identifiers', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 503, { success: false, error: 'Hardcover response timed out', error_code: 'hardcover_import_unconfirmed', outcome: 'unconfirmed', data: {
        audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
    } });
    await app.submitEditionCreate();

    const html = app.renderEditionDialog(dialog);
    assert.match(html, /Hardcover’s import result is still unconfirmed/);
    assert.match(html, /data-edition-dialog="check-import"/);
    assert.match(html, /hardcover\.app\/book\/42/);
    assert.doesNotMatch(html, /data-edition-dialog="confirm-create"/);
    assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', needsReview.book_id).recovery.recoveryToken, 'opaque-token');
});

test('tokenless ambiguous ebook create persists manual recovery and never offers another create', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 502, { success: false, outcome: 'unconfirmed', error_code: 'hardcover_import_unconfirmed', error: 'Import response was ambiguous', data: { hardcover_book_id: '42' } });
    dialog.record = { ...needsReview, format: 'ebook' };
    dialog.draft = { ...dialog.draft, reading_format: 'ebook', ebook_candidate: { title: 'Dune', isbn_13: '9780000000002' } };
    await app.submitEditionCreate();
    assert.equal(dialog.outcome, 'transport_unknown');
    let html = app.renderEditionDialog(dialog);
    assert.match(html, /Inspect Hardcover/);
    assert.match(html, /run a new sync/);
    assert.match(html, /HTTP 502.*hardcover_import_unconfirmed/);
    assert.doesNotMatch(html, /check-import|confirm-create|retry-create/);
    let creates = 0;
    app.fetchJsonWithTimeout = async () => { creates++; throw new Error('Must not submit another import'); };
    await app.submitEditionCreate();
    assert.equal(creates, 0);

    const saved = app.loadPendingEditionRecovery('p1', 'run-1', 'li_1');
    saved.outcome = 'unconfirmed';
    saved.recovery = null;
    saved.errorHttpStatus = 0;
    saved.errorCode = '';
    window.sessionStorage.setItem(app.pendingEditionRecoveryKey('p1', 'run-1', 'li_1'), JSON.stringify(saved));
    app.pendingEditionRecoveries.clear();
    const reloaded = createApp();
    const recovered = reloaded.loadPendingEditionRecovery('p1', 'run-1', 'li_1');
    assert.equal(recovered.outcome, 'transport_unknown');
    reloaded.editionDialog = { ...dialog, ...recovered };
    html = reloaded.renderEditionDialog(reloaded.editionDialog);
    assert.match(html, /run a new sync/);
    assert.match(html, /HTTP 502.*hardcover_import_unconfirmed/);
    assert.doesNotMatch(html, /check-import|confirm-create|retry-create/);
});

test('check import status uses the saved token and original identifiers, then records recovered success', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 503, { success: false, error_code: 'hardcover_import_unconfirmed', outcome: 'unconfirmed', data: {
        audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
    } });
    app.openSummary.runContext.runId = 'run-1';
    app.openSummary.addedEditionBookIds = new Set();
    app.refreshEditionActionStates = () => {};
    app.saveAddedEditionBookId = (...args) => { app.persistedAdded = args; };
    await app.submitEditionCreate();
    dialog.fieldValues = { title: 'Edited after submission' };
    let captured;
    app.fetchJsonWithTimeout = async (url, options) => {
        captured = { url, options, body: JSON.parse(options.body) };
        return { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } };
    };

    await app.checkEditionImport();

    assert.equal(captured.url, '/api/profiles/p1/edition-drafts/check-import');
    assert.deepEqual(captured.body, {
        run_id: 'run-1', abs_item_id: 'li_1', audible_identifier: 'B00ABC1234:us', recovery_token: 'opaque-token'
    });
    assert.equal(captured.body.resync, undefined);
    assert.equal(dialog.result.hardcover_edition_id, '99');
    assert.ok(app.openSummary.addedEditionBookIds.has('li_1'));
    assert.deepEqual(app.persistedAdded, ['p1', 'run-1', 'li_1']);
    assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
    assert.match(app.renderEditionDialog(dialog), /The match is saved for the next sync/);
});

test('terminal failed status check clears recovery and prevents another create', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 503, { success: false, outcome: 'unconfirmed', data: {
        audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
    } });
    await app.submitEditionCreate();
    app.fetchJsonWithTimeout = async () => ({ response: { ok: false, status: 502 }, data: {
        success: false, outcome: 'failed', error_code: 'hardcover_import_failed', error: 'Hardcover reported import failure'
    } });

    await app.checkEditionImport();

    assert.equal(dialog.outcome, 'failed');
    assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
    assert.match(app.renderEditionDialog(dialog), /Hardcover reported import failure/);
    assert.match(app.renderEditionDialog(dialog), /HTTP 502.*hardcover_import_failed/);
    assert.match(app.renderEditionDialog(dialog), /Another create is disabled/);
    assert.match(app.renderEditionDialog(dialog), /confirm-create" disabled/);
});

test('invalid recovery token and stale create conflict persist as unknown and direct a fresh sync', async () => {
    const terminalErrors = [
        { error_code: 'edition_recovery_invalid', error: 'The recovery token is no longer valid.' },
        { error_code: 'edition_create_conflict', error: 'The source record changed.' }
    ];
    for (const terminalError of terminalErrors) {
        const app = editionApp();
        const dialog = stubDialog(app, 503, { success: false, outcome: 'unconfirmed', data: {
            audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
        } });
        app.openSummary.records.set(String(needsReview.book_id), needsReview);
        await app.submitEditionCreate();
        app.fetchJsonWithTimeout = async () => ({ response: { ok: false, status: 409 }, data: {
            success: false, outcome: 'not_submitted', ...terminalError
        } });

        await app.checkEditionImport();

        assert.equal(dialog.outcome, 'transport_unknown');
        assert.equal(dialog.recovery, null);
        let html = app.renderEditionDialog(dialog);
        assert.match(html, /run a new sync to refresh the match/);
        assert.match(html, /This status check submitted no new import/);
        assert.match(html, /Open Hardcover/);
        assert.ok(html.includes(`HTTP 409 · Error code: ${terminalError.error_code}`));
        assert.doesNotMatch(html, /data-edition-dialog="check-import"|data-edition-dialog="confirm-create"/);
        assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', 'li_1').outcome, 'transport_unknown');

        app.editionDialog = null;
        await app.openEditionDialog(needsReview.book_id);
        html = app.renderEditionDialog(app.editionDialog);
        assert.equal(app.editionDialog.outcome, 'transport_unknown');
        assert.match(html, /run a new sync to refresh the match/);
        assert.doesNotMatch(html, /data-edition-dialog="check-import"|data-edition-dialog="confirm-create"/);
    }
});

test('created but unsaved edition offers only token-backed match recovery', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 502, { success: false, error: 'Local save failed', error_code: 'edition_association_save_failed', outcome: 'created', data: {
        audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
    } });
    await app.submitEditionCreate();
    const html = app.renderEditionDialog(dialog);
    assert.match(html, /Edition created; match not saved/);
    assert.match(html, /data-edition-dialog="check-import"/);
    assert.doesNotMatch(html, /data-edition-dialog="confirm-create"/);
});

test('created ebook without a recovery token clearly reports the saved-match failure and blocks create', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 502, { success: false, error: 'Local save failed', outcome: 'created', data: { hardcover_book_id: '42' } });
    dialog.record = { ...needsReview, format: 'ebook' };
    dialog.draft = { ...dialog.draft, reading_format: 'ebook' };
    await app.submitEditionCreate();
    const html = app.renderEditionDialog(dialog);
    assert.equal(dialog.outcome, 'created');
    assert.match(html, /Edition created; match not saved/);
    assert.match(html, /Local save failed/);
    assert.doesNotMatch(html, /check-import|confirm-create|retry-create/);
});

test('closing an ambiguous import and reopening the item restores recovery without a new create', async () => {
    const app = editionApp();
    const dialog = stubDialog(app, 503, { success: false, outcome: 'unconfirmed', data: {
        audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
    } });
    await app.submitEditionCreate();
    app.editionDialog = null;
    app.openSummary.records.set(String(needsReview.book_id), needsReview);
    app.openSummary.editionCapabilityLoaded = false;

    await app.openEditionDialog(needsReview.book_id);

    assert.equal(app.editionDialog.outcome, 'unconfirmed');
    assert.match(app.renderEditionDialog(app.editionDialog), /data-edition-dialog="check-import"/);
    assert.doesNotMatch(app.renderEditionDialog(app.editionDialog), /data-edition-dialog="confirm-create"/);
});

test('not-submitted create response offers retry while generic proxy errors and transport timeouts do not', async () => {
    for (const message of [
        'The request took too long to add this edition. Nothing was added to Hardcover. Please try again.',
        "Hardcover's daily API quota is running low. Nothing was added to Hardcover. Please wait until Hardcover's daily API quota resets, then try again."
    ]) {
        const safeRetry = editionApp();
        const retryDialog = stubDialog(safeRetry, 503, { success: false, error: message, error_code: 'edition_create_not_submitted', outcome: 'not_submitted' });
        await safeRetry.submitEditionCreate();
        const html = safeRetry.renderEditionDialog(retryDialog);
        assert.ok(html.includes(safeRetry.escapeHtml(message)));
        assert.match(html, /data-edition-dialog="retry-create"[^>]*>Retry add edition<\/button>/);
        assert.match(html, /class="edition-form"/);
    }

    const proxyError = editionApp();
    const unknownDialog = stubDialog(proxyError, 503, {});
    await proxyError.submitEditionCreate();
    const html = proxyError.renderEditionDialog(unknownDialog);
    assert.match(html, /The import result is unknown/);
    assert.match(html, /HTTP 503/);
    assert.match(html, /Open Hardcover/);
    assert.doesNotMatch(html, /data-edition-dialog="confirm-create"|data-edition-dialog="check-import"|Retry add edition/);

    const timeout = editionApp();
    const timeoutDialog = stubDialog(timeout, 0, {});
    timeout.fetchJsonWithTimeout = async () => { throw new Error('The request timed out'); };
    await timeout.submitEditionCreate();
    assert.match(timeout.renderEditionDialog(timeoutDialog), /The request timed out before the app received a result/);
    assert.doesNotMatch(timeout.renderEditionDialog(timeoutDialog), /data-edition-dialog="confirm-create"|data-edition-dialog="check-import"/);
});

test('readable 503 and unusable success envelopes remain unknown and block another import', async () => {
    const readable503 = editionApp();
    const readableDialog = stubDialog(readable503, 503, { success: false, error: 'Service Unavailable' });
    await readable503.submitEditionCreate();
    const readableHTML = readable503.renderEditionDialog(readableDialog);
    assert.match(readableHTML, /Service Unavailable/);
    assert.match(readableHTML, /import result is unknown/);
    assert.match(readableHTML, /HTTP 503/);
    assert.doesNotMatch(readableHTML, /confirm-create/);

    const unusable200 = editionApp();
    const unusableDialog = stubDialog(unusable200, 200, { success: false });
    unusable200.fetchJsonWithTimeout = async () => ({ response: { ok: true, status: 200 }, data: { success: false } });
    await unusable200.submitEditionCreate();
    const unusableHTML = unusable200.renderEditionDialog(unusableDialog);
    assert.match(unusableHTML, /import result is unknown/);
    assert.match(unusableHTML, /HTTP 200/);
    assert.doesNotMatch(unusableHTML, /confirm-create/);
});

test('import recovery preserves auth expiry handling and escapes server supplied title', async () => {
    const app = editionApp();
    app.handleAuthExpiry = () => { app.authExpiryCalls = (app.authExpiryCalls || 0) + 1; };
    const dialog = stubDialog(app, 503, { success: false, outcome: 'unconfirmed', data: {
        audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token', title: '<img src=x onerror=alert(1)>'
    } });
    await app.submitEditionCreate();
    assert.match(app.renderEditionDialog(dialog), /&lt;img src=x onerror=alert\(1\)&gt;/);
    assert.doesNotMatch(app.renderEditionDialog(dialog), /<img src=x/);
    app.fetchJsonWithTimeout = async () => ({ response: { ok: false, status: 401 }, data: { success: false } });
    await app.checkEditionImport();
    assert.equal(app.authExpiryCalls, 1);
    assert.equal(app.editionDialog, null);
});
