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

// Normalize rendered text for assertions, not raw-HTML matching: strip tags, decode common entities once, and normalize whitespace.
function visibleText(html) {
    return html
        .replace(/<[^>]*>/g, ' ')
        .replace(/&nbsp;/g, ' ')
        .replace(/&lt;/g, '<')
        .replace(/&gt;/g, '>')
        .replace(/&quot;/g, '"')
        .replace(/&#39;/g, "'")
        .replace(/&amp;/g, '&')
        .replace(/\s+/g, ' ')
        .trim();
}

function addEditionButton(html) {
    const button = [...html.matchAll(/<button\b[^>]*>/g)]
        .map(([tag]) => tag)
        .find(tag => /\bdata-edition-action=(["'])add\1/.test(tag));
    assert.ok(button, 'Add edition button should be present');
    const attributes = button.replace(/(["'])[\s\S]*?\1/g, '');
    return {
        disabled: /\sdisabled(?=\s|=|>)/.test(attributes),
        title: visibleText(button.match(/\btitle=(["'])(.*?)\1/)?.[2] || '')
    };
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

    const text = visibleText(html);
    assert.match(text, /Run started:/);
    assert.match(text, /Last activity:/);
    assert.match(text, /Previous successful sync:/);
    assert.doesNotMatch(text, /Last attempted:/);
    assert.doesNotMatch(text, /Last successful:/);
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

    const text = visibleText(html);
    assert.match(text, /Run started:/);
    assert.match(text, /Completed:/);
    assert.doesNotMatch(text, /Last attempted:/);
    assert.doesNotMatch(text, /Last successful:/);
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

        const text = visibleText(html);
        assert.ok(text.includes(`${terminal.label}:`));
        assert.match(text, /Last successful:/);
        assert.doesNotMatch(text, /Last attempted:/);
    });
}

test('attempt and success timestamps remain as fallbacks without retained run details', () => {
    const app = createApp();
    const html = app.renderStatusCard('profile-1', {
        profile_id: 'profile-1',
        last_attempted_at: '2026-09-17T12:00:00Z',
        last_successful_at: '2026-09-16T12:30:00Z'
    });

    const text = visibleText(html);
    assert.match(text, /Last attempted:/);
    assert.match(text, /Last successful:/);
    assert.doesNotMatch(text, /Run started:/);
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

test('session reset closes pending preview and status requests and ignores their late responses', async t => {
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
    app.activeStatusRequests = 0;
    const statusLoading = app.loadStatuses({ silent: true });

    app.resetSessionBoundState();
    assert.equal(app.editionDialog, null);
    assert.deepEqual(app.users, []);
    assert.deepEqual(Object.keys(app.statuses), []);
    assert.equal(app.openSummary, null);
    assert.equal(previewController.signal.aborted, true);
    assert.equal(content.innerHTML, '');
    assert.equal(modal.style.display, 'none');

    const newSessionDialog = { mode: 'forget', record: { book_id: 'new-session-item' } };
    app.editionDialog = newSessionDialog;
    app.users = [{ id: 'new-session-profile' }];
    const newSessionStatuses = { 'new-session-profile': { profile_id: 'new-session-profile' } };
    app.statuses = newSessionStatuses;
    content.innerHTML = 'new session dialog';
    modal.style.display = 'block';
    const writesForNewSession = harness.htmlWrites;

    pending.find(item => item.url.includes('/edition-drafts/source/')).resolve({
        response: { ok: true, status: 200 }, data: { success: true, data: { private: 'draft' } }
    });
    pending.find(item => item.url.includes('/edition-capability')).resolve({ response: { ok: true, status: 200 }, data: { success: true, data: {} } });
    pending.find(item => item.url === '/api/status').resolve({
        response: { ok: true, status: 200 },
        data: { success: true, data: [{ profile_id: 'private-profile', profile_name: 'Private profile' }] }
    });
    await Promise.all([loading, statusLoading]);
    assert.equal(app.editionDialog, newSessionDialog);
    assert.equal(app.statuses, newSessionStatuses, 'a prior session must not replace the new session status');
    assert.deepEqual(app.users, [{ id: 'new-session-profile' }]);
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

const audibleImportRecord = {
    book_id: 'li_asin', outcome: 'needs_review', reason: 'audible_import_available',
    title: 'ABS title', author: 'ABS author', asin: 'B00ABC1234', format: 'audiobook'
};

function confirmedAudibleDraft(overrides = {}) {
    return {
        abs_item_id: 'li_asin', reading_format: 'audiobook', eligible: true, dry_run: false,
        region_status: 'confirmed', confirmed_region: 'uk',
        source_identifiers: { asin: 'B00ABC1234' },
        audible_identifier_candidate: { asin: 'B00ABC1234', region: 'uk', correction_allowed: true },
        metadata_preview: {
            title: 'ABS title', subtitle: 'ABS subtitle', author: 'ABS author', narrator: 'ABS narrator',
            series: 'ABS series', series_position: '1', publisher: 'ABS publisher', release_date: '2024-01-01',
            audio_seconds: 3600, language: 'English', cover_url: 'https://abs.example/cover'
        },
        audnexus_record: {
            asin: 'B00ABC1234', title: 'Audnexus title', subtitle: 'Audnexus subtitle', authors: ['Audnexus author'],
            narrators: ['Audnexus narrator'], series: ['Audnexus series'], series_position: '2',
            publisher: 'Audnexus publisher', release_date: '2024-02-01', runtime_seconds: 3660,
            language: 'English', cover_url: 'https://audnexus.example/cover'
        },
        audnexus_comparison: {
            title: 'differs', subtitle: 'match', authors: 'differs', narrators: 'match', series: 'differs',
            series_position: 'differs', publisher: 'differs', release_date: 'differs', runtime: 'differs',
            language: 'match', cover_url: 'differs'
        },
        ...overrides
    };
}

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
    assert.match(app.editionCreateIneligibleReason({ ...needsReview, asin: '', isbn: '' }, ctx), /valid ASIN or ISBN from Audiobookshelf/);
    assert.equal(app.editionCreateIneligibleReason({ ...needsReview, asin: '', isbn: '9780306406157' }, ctx), null);
    assert.equal(app.editionCreateIneligibleReason({ ...needsReview, asin: 'bad', isbn: '0-306-40615-2' }, ctx), null);
    assert.equal(app.editionCreateIneligibleReason({ ...needsReview, asin: 'bad', isbn: '0–306–40615–2' }, ctx), null);
    assert.match(app.editionCreateIneligibleReason({ ...needsReview, asin: 'bad', isbn: 'not-an-isbn' }, ctx), /valid ASIN or ISBN from Audiobookshelf/);
    assert.equal(app.editionCreateIneligibleReason({ ...needsReview, asin: 'B00ABC1234', isbn: 'not-an-isbn' }, ctx), null);
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
    assert.equal(addEditionButton(html).disabled, true);
    assert.match(addEditionButton(html).title, /Hardcover book.*matched/);
    const noIdentifiers = app.renderEditionActions({ ...needsReview, asin: '', isbn: '' });
    assert.equal(addEditionButton(noIdentifiers).disabled, true);
    assert.match(addEditionButton(noIdentifiers).title, /ASIN.*ISBN.*Audiobookshelf/);
    const isbnOnlyAudio = app.renderEditionActions({ ...needsReview, asin: '', isbn: '9780306406157' });
    assert.match(isbnOnlyAudio, /data-edition-action="add"/);
    assert.doesNotMatch(isbnOnlyAudio, /disabled/);
});

test('unavailable edition state fails closed until a healthy details fetch restores actions', async t => {
    const previousDocument = global.document;
    const content = { innerHTML: '', querySelectorAll() { return []; } };
    const tabs = { innerHTML: '' };
    global.document = { ...previousDocument, getElementById(id) {
        if (id === 'sync-summary-content') return content;
        if (id === 'sync-summary-tabs') return tabs;
        if (id === 'sync-summary-container') return { style: {} };
        return null;
    } };
    t.after(() => { global.document = previousDocument; });

    const app = editionApp();
    const record = { ...needsReview, edition_added: true };
    app.openSummary.records.set(record.book_id, record);
    app.openSummary.addedEditionBookIds = new Set([record.book_id]);
    app.renderDetailsSnapshot({
        run_id: 'run-1', state: 'completed', edition_actions_unavailable: true,
        outcome_counts: { needs_review: 1 }, book_outcomes: [record]
    });
    assert.equal(app.openSummary.runContext.editionActionsUnavailable, true);
    assert.match(content.innerHTML, /saved sync state could not be read.*inspect Hardcover manually/);
    assert.doesNotMatch(app.renderEditionActions(record), /data-edition-action|Hardcover Edition Added/);
    await app.openEditionDialog(record.book_id);
    await app.openForgetDialog(record.book_id);
    assert.equal(app.editionDialog, undefined);

    app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', outcome_counts: {}, book_outcomes: [needsReview] });
    assert.equal(app.openSummary.runContext.editionActionsUnavailable, false);
    assert.match(app.renderEditionActions(needsReview), /data-edition-action="add"/);
});

test('open edition dialogs fail closed when unavailable details arrive', async t => {
    const previousDocument = global.document;
    const content = { innerHTML: '', querySelectorAll() { return []; } };
    const tabs = { innerHTML: '' };
    global.document = { ...previousDocument, getElementById(id) {
        if (id === 'sync-summary-content') return content;
        if (id === 'sync-summary-tabs') return tabs;
        if (id === 'sync-summary-container') return { style: {} };
        return null;
    } };
    t.after(() => { global.document = previousDocument; });

    const cases = [
        ['create', { mode: 'create', draft: { reading_format: 'audiobook' }, outcome: '' }, app => app.submitEditionCreate()],
        ['check import', { mode: 'create', draft: { reading_format: 'audiobook' }, outcome: 'unconfirmed', recovery: { recoveryToken: 'token', runId: 'run-1', absItemId: 'li_1' } }, app => app.checkEditionImport()],
        ['forget', { mode: 'forget', record: { book_id: 'li_1', hardcover_book_id: '42' } }, app => app.submitForget()]
    ];
    for (const [name, fields, action] of cases) {
        const app = editionApp();
        let requests = 0;
        app.readEditionFormFields = () => ({});
        app.fetchJsonWithTimeout = async () => {
            requests++;
            return { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } };
        };
        app.editionDialog = {
            profileId: 'p1', runId: 'run-1', record: needsReview, busy: true,
            ...fields
        };
        const dialog = app.editionDialog;
        app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', edition_actions_unavailable: true, outcome_counts: {}, book_outcomes: [needsReview] });
        assert.equal(app.editionDialog, dialog, `${name} remains tracked while in flight`);
        assert.match(app.renderEditionDialog(dialog), /Edition actions are unavailable/);
        assert.doesNotMatch(app.renderEditionDialog(dialog), /data-edition-dialog="(?:confirm-create|check-import|confirm-forget)"/);
        dialog.busy = false;
        await action(app);
        assert.equal(requests, 0, `${name} must not start while saved state is unavailable`);

        app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', outcome_counts: {}, book_outcomes: [needsReview] });
        assert.equal(dialog.editionActionsUnavailable, false);
        await action(app);
        assert.equal(requests, 1, `${name} is usable again after a healthy details fetch`);
    }

    const app = editionApp();
    app.editionDialog = { mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, draft: { reading_format: 'audiobook' }, busy: false };
    app.renderDetailsSnapshot({ run_id: 'run-1', state: 'completed', edition_actions_unavailable: true, outcome_counts: {}, book_outcomes: [needsReview] });
    assert.equal(app.editionDialog, null, 'an idle dialog closes when details become unavailable');
});

test('Add edition waits for the profile capability and disables only a confirmed format denial', () => {
    const app = editionApp();
    app.openSummary.editionCapabilityLoaded = false;
    let html = app.renderEditionActions(needsReview);
    assert.equal(addEditionButton(html).disabled, true);
    assert.match(addEditionButton(html).title, /Checking/);

    app.openSummary.editionCapabilityLoaded = true;
    app.openSummary.editionCapability = {
        audiobook: { status: 'denied', can_attempt: false, reason: 'insufficient_scope' },
        ebook: { status: 'allowed', can_attempt: true }
    };
    html = app.renderEditionActions(needsReview);
    assert.equal(addEditionButton(html).disabled, true);
    assert.match(addEditionButton(html).title, /permission/);

    const ebook = app.renderEditionActions({ ...needsReview, format: 'ebook' });
    assert.doesNotMatch(ebook, /disabled/);
});

test('audiobook capability evidence follows the selected ASIN or ISBN insertion path', () => {
    const app = editionApp();
    const capability = {
        ebook: { status: 'allowed', can_attempt: true },
        audiobook_isbn: { status: 'denied', can_attempt: false, reason: 'insufficient_scope' },
        audiobook: { status: 'allowed', can_attempt: true }
    };
    const denied = app.editionCapabilityGate(capability, 'audiobook', '');
    assert.equal(denied.blocked, true);
    assert.match(denied.reason, /permission/);
    assert.deepEqual(app.editionCapabilityGate(capability, 'audiobook', 'B00ABC1234'), { blocked: false });
    assert.deepEqual(app.editionCapabilityGate({
        ebook: { status: 'allowed', can_attempt: true },
        audiobook_isbn: { status: 'allowed', can_attempt: true },
        audiobook: { status: 'denied', can_attempt: false, reason: 'no scope' }
    }, 'audiobook', 'B00ABC1234'), { blocked: true, reason: 'no scope' });

    app.openSummary.editionCapability = capability;
    const isbnRecord = { ...needsReview, asin: 'malformed', isbn: '9780306406157' };
    const isbnButton = addEditionButton(app.renderEditionActions(isbnRecord));
    assert.equal(isbnButton.disabled, true);
    assert.match(isbnButton.title, /permission/);
    app.openSummary.editionCapability = { ...capability, audiobook_isbn: { status: 'allowed', can_attempt: true } };
    assert.doesNotMatch(app.renderEditionActions(isbnRecord), /disabled/);
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
        const pendingButton = addEditionButton(app.renderEditionActions(record));
        assert.equal(pendingButton.disabled, true);
        assert.match(pendingButton.title, /Checking/);
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

    const refreshButton = { disabled: false, textContent: 'Refresh permissions' };
    const content = {
        innerHTML: '',
        querySelector: selector => selector === '[data-edition-capability-refresh]' ? refreshButton : null,
        querySelectorAll: () => []
    };
    const tabs = { innerHTML: '' };
    const previousDocument = global.document;
    global.document = {
        ...previousDocument,
        getElementById(id) { return id === 'sync-summary-content' ? content : id === 'sync-summary-tabs' ? tabs : null; }
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

test('permission refresh follows current capability dry-run state instead of the historical run', async () => {
    const app = editionApp();
    const summary = app.openSummary;
    summary.runContext.dryRun = true;
    summary.editionCapability = { audiobook: { status: 'denied', can_attempt: false }, dry_run: false };
    const previousDocument = global.document;
    global.document = { ...previousDocument, querySelector: () => null };
    try {
        let request;
        app.profileUrl = (profileId, path) => `/api/profiles/${profileId}${path}`;
        app.fetchJsonWithTimeout = (url, options) => {
            request = { url, options };
            return Promise.resolve({
                response: { ok: true, status: 200 },
                data: { success: true, data: { audiobook: { status: 'allowed', can_attempt: true }, dry_run: false } }
            });
        };

        await app.refreshOpenEditionCapability();

        assert.equal(request.url, '/api/profiles/p1/edition-capability/refresh');
        assert.equal(request.options.method, 'POST');
        assert.equal(summary.editionCapability.audiobook.status, 'allowed');
    } finally {
        global.document = previousDocument;
    }
});

test('pending permission refresh survives replacement of the run summary', async () => {
    const app = editionApp();
    const summary = app.openSummary;
    summary.runId = 'run-1';
    summary.editionCapability = {
        audiobook: { status: 'denied', can_attempt: false, reason: 'insufficient_scope' },
        dry_run: false
    };
    app.statuses.p1.snapshot.run_id = 'run-2';
    app.renderDetailsState = () => {};
    app.profileUrl = profileId => `/profiles/${profileId}`;
    const previousDocument = global.document;
    const content = { querySelectorAll: () => [] };
    const container = { style: {} };
    global.document = {
        ...previousDocument,
        activeElement: null,
        getElementById: id => id === 'sync-summary-content' ? content : id === 'sync-summary-container' ? container : null,
        querySelector: () => null
    };
    const pendingCapability = deferred();
    const oldRunDetails = deferred();
    const newRunDetails = deferred();
    const detailRequests = [];
    app.fetchJsonWithTimeout = (url, options) => {
        if (options?.method === 'POST') return pendingCapability.promise;
        detailRequests.push(url);
        if (url.endsWith('/runs/run-1/details')) {
            options.signal.addEventListener('abort', () => {
                const error = new Error('aborted');
                error.name = 'AbortError';
                oldRunDetails.resolve(Promise.reject(error));
            });
            return oldRunDetails.promise;
        }
        return newRunDetails.promise;
    };

    try {
        const refresh = app.refreshOpenEditionCapability();
        const oldDetails = app.fetchAndRenderDetails();
        const updatedSummary = app.refreshOpenSummary();
        assert.equal(app.openSummary, summary);
        assert.equal(summary.runId, 'run-2');
        assert.deepEqual(detailRequests, [
            '/profiles/p1/runs/run-1/details',
            '/profiles/p1/runs/run-2/details'
        ]);
        await oldDetails;
        assert.equal(summary.loading, true, 'finishing the aborted old request must not clear the new request loading state');

        newRunDetails.resolve({ response: { ok: false, status: 500 }, data: {} });
        await updatedSummary;

        pendingCapability.resolve({
            response: { ok: true, status: 200 },
            data: { success: true, data: { audiobook: { status: 'allowed', can_attempt: true }, dry_run: false } }
        });
        await refresh;

        assert.equal(summary.editionCapability.audiobook.status, 'allowed');
        assert.equal(summary.editionCapabilityLoaded, true);
    } finally {
        oldRunDetails.resolve({ response: { ok: false, status: 500 }, data: {} });
        newRunDetails.resolve({ response: { ok: false, status: 500 }, data: {} });
        global.document = previousDocument;
    }
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

test('the Add edition button renders inside the Hardcover candidate region', () => {
    const app = editionApp();
    const record = { ...needsReview, hardcover_title: 'Dune', hardcover_slug: 'dune' };
    const html = app.renderOutcomeRecord(record);
    const candidate = html.match(/<section\b(?=[^>]*aria-label="Hardcover candidate")[^>]*>[\s\S]*?<\/section>/)?.[0] || '';
    assert.ok(candidate, 'Hardcover candidate region should be present');
    assert.match(candidate, /data-edition-action="add"/, 'Add edition action should belong to the candidate region');
});

test('an Audible import row reuses the Add edition pill without a Hardcover candidate', () => {
    const app = editionApp();
    const record = { ...audibleImportRecord };
    app.openSummary.records.set(record.book_id, record);
    const html = app.renderOutcomeRecord(record);
    assert.equal(app.editionCreateIneligibleReason(record, app.openSummary.runContext), null);
    assert.match(html, /data-edition-action="add"/);
    assert.match(html, /book-service-link edition-action-pill/);
    assert.doesNotMatch(html, /Hardcover candidate|hardcover-candidate/);
    assert.doesNotMatch(html, /<strong>Hardcover:<\/strong>/);
});

for (const runState of ['completed', 'canceled']) {
    test(`an unmatched Audible ASIN audiobook can be previewed from a ${runState} run`, async t => {
        const record = {
            book_id: '23aefd9f-3f65-4fb1-9c19-8918919fcc09',
            outcome: 'needs_review',
            reason: 'audible_import_available',
            match_method: 'audible_asin',
            title: "The Magician's Land",
            author: 'Lev Grossman',
            asin: 'B00K8F87C0',
            format: 'audiobook'
        };
        const app = editionApp();
        app.openSummary.runContext.state = runState;
        app.statuses.p1.snapshot.state = runState;
        app.openSummary.records.set(record.book_id, record);

        const row = app.renderOutcomeRecord(record);
        assert.equal(app.editionCreateIneligibleReason(record, app.openSummary.runContext), null);
        assert.match(row, /data-edition-action="add"(?![^>]*disabled)/);
        assert.match(row, /<strong>Reason:<\/strong> No Hardcover match found\. Import by Audible ASIN is available\./);
        assert.match(row, /<strong>Match method:<\/strong> Audible ASIN/);
        assert.doesNotMatch(row, /audible_import_available|audible_asin/);

        const previousDocument = global.document;
        t.after(() => { global.document = previousDocument; });
        const summaryClickHandlers = [];
        const summaryContent = {
            addEventListener(type, listener) {
                if (type === 'click') summaryClickHandlers.push(listener);
            }
        };
        const element = { style: {}, addEventListener() {}, replaceChildren() {} };
        global.document = {
            ...previousDocument,
            getElementById: id => id === 'sync-summary-content' ? summaryContent : element,
            querySelectorAll: () => []
        };

        const requests = [];
        app.fetchJsonWithTimeout = async (url, options = {}) => {
            requests.push({ url, method: options.method || 'GET' });
            if (url.endsWith('/edition-capability')) {
                return { response: { ok: true, status: 200 }, data: { success: true, data: {} } };
            }
            const baseDraft = confirmedAudibleDraft();
            return {
                response: { ok: true, status: 200 },
                data: { success: true, data: confirmedAudibleDraft({
                    abs_item_id: record.book_id,
                    source_identifiers: { asin: record.asin },
                    audible_identifier_candidate: { asin: record.asin, region: 'us', correction_allowed: true },
                    confirmed_region: 'us',
                    source_metadata_preview: { ...baseDraft.metadata_preview, release_date: '2014-08-04' },
                    metadata_preview: { ...baseDraft.metadata_preview, release_date: '2014-08-05' },
                    audnexus_record: { ...baseDraft.audnexus_record, asin: record.asin, release_date: '2014-08-05' }
                }) }
            };
        };

        app.setupEventListeners();
        const article = { dataset: { bookId: record.book_id } };
        const button = {
            dataset: { editionAction: 'add' },
            disabled: /data-edition-action="add"[^>]*disabled/.test(row),
            closest(selector) {
                if (selector === 'button[data-edition-action]') return this;
                if (selector === '[data-book-id]') return article;
                return null;
            }
        };
        const openPromises = [];
        const openEditionDialog = app.openEditionDialog.bind(app);
        app.openEditionDialog = bookId => {
            const opening = openEditionDialog(bookId);
            openPromises.push(opening);
            return opening;
        };
        for (const handler of summaryClickHandlers) handler({ target: button });
        assert.equal(openPromises.length, 1, 'the enabled row action should reach the delegated Add edition handler');
        await openPromises[0];

        assert.equal(app.editionDialog.audibleImport, true);
        assert.equal(requests.find(request => request.url.includes('/edition-drafts/source/'))?.url,
            `/api/profiles/p1/edition-drafts/source/${record.book_id}`);
        const dialogHTML = app.renderEditionDialog(app.editionDialog);
        assert.match(dialogHTML, /data-edition-dialog="confirm-create"/);
        assert.doesNotMatch(dialogHTML, /Regional Audible identifier|ASIN:region|audible_identifier_preview|preview-audible/);
        const releaseDateRow = dialogHTML.match(/<div\b(?=[^>]*\bdata-comparison="release_date")[^>]*>[\s\S]*?<\/div>/)?.[0] || '';
        assert.match(releaseDateRow, /Audiobookshelf<\/span>2014-08-04/);
        assert.match(releaseDateRow, /Audnexus\/Audible<\/span>2014-08-05/);
        assert.match(releaseDateRow, />Different</);
        assert.ok(requests.every(request => request.method === 'GET'));
        assert.equal(requests.some(request => request.url.endsWith('/edition-drafts/create')), false);
    });
}

test('outcome match methods render as readable labels and humanize unknown methods', () => {
    const app = editionApp();
    const cases = [
        ['title_author', 'Title and author'],
        ['audible_asin', 'Audible ASIN'],
        ['edition_asin', 'Edition ASIN fallback'],
        ['isbn_13', 'ISBN-13'],
        ['asin', 'ASIN'],
        ['isbn', 'ISBN'],
        ['unfamiliar_method_value', 'Unfamiliar method value']
    ];
    for (const [match_method, label] of cases) {
        const html = app.renderOutcomeRecord({ ...needsReview, match_method });
        assert.ok(html.includes(`<strong>Match method:</strong> ${label}`), `${match_method} should render as ${label}`);
    }
});

test('Audible import modal shows every comparison and escapes ABS and Audnexus values', () => {
    const app = editionApp();
    const draft = confirmedAudibleDraft({
        metadata_preview: { ...confirmedAudibleDraft().metadata_preview, title: '<img src=x onerror=alert(1)>' },
        audnexus_record: { ...confirmedAudibleDraft().audnexus_record, title: '<script>bad()</script>' }
    });
    const dialog = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
        loading: false, busy: false, runDryRun: false, capability: null, draft
    };
    const html = app.renderEditionDialog(dialog);
    for (const key of ['title', 'subtitle', 'authors', 'narrators', 'series', 'series_position', 'publisher', 'release_date', 'runtime', 'language']) {
        assert.match(html, new RegExp(`data-comparison="${key}"`));
    }
    assert.doesNotMatch(html, /data-comparison="cover_url"|Cover URL/);
    assert.match(html, /Different/);
    assert.match(html, /&lt;img src=x onerror=alert\(1\)&gt;/);
    assert.match(html, /&lt;script&gt;bad\(\)&lt;\/script&gt;/);
    assert.doesNotMatch(html, /<img src=x|<script>bad/);
    assert.doesNotMatch(html, /Hardcover candidate|type="checkbox"/);
    assert.match(html, /data-edition-dialog="confirm-create"/);
    assert.match(html, /<h4>Audnexus\/Audible<\/h4>/);
    assert.match(html, /audible-comparison-source">Audnexus\/Audible<\/span>/);
});

test('audiobook identifier fallback is limited to completed audiobook outcomes with a valid preferred source ASIN', () => {
    const app = editionApp();
    for (const match_method of ['edition_asin', 'isbn']) {
        const fallback = {
            book_id: 'li_fallback', outcome: 'synced', reason: 'original reason', match_method,
            title: 'Fallback audiobook', format: 'audiobook', source_asin: 'B00SOURCE1', asin: 'invalid',
            hardcover_book_id: '42'
        };
        for (const outcome of ['synced', 'already_current', 'skipped']) {
            assert.equal(app.editionCreateIneligibleReason({ ...fallback, outcome }, app.openSummary.runContext), null);
        }
        for (const record of [
            { ...fallback, outcome: 'would_sync' },
            { ...fallback, outcome: 'failed' },
            { ...fallback, format: 'ebook' },
            { ...fallback, source_asin: 'bad' },
            { ...fallback, source_asin: '' },
            { ...fallback, match_method: 'saved_match' }
        ]) {
            assert.ok(app.editionCreateIneligibleReason(record, app.openSummary.runContext), JSON.stringify(record));
            assert.doesNotMatch(app.renderOutcomeRecord(record), /data-edition-action="add"/, JSON.stringify(record));
        }
        assert.equal(app.editionCreateIneligibleReason({ ...fallback, source_asin: null, asin: 'B00FALLBK1' }, app.openSummary.runContext), null);
        const row = app.renderOutcomeRecord(fallback);
        if (match_method === 'edition_asin') assert.match(row, /<strong>Match method:<\/strong> Edition ASIN fallback/);
        assert.match(row, /Hardcover target: book 42/);
        assert.match(row, /data-edition-action="add"/);
        assert.match(row, /data-edition-action="forget"/);
    }

    const fallback = { book_id: 'li_fallback', outcome: 'synced', match_method: 'edition_asin', format: 'audiobook', source_asin: 'B00SOURCE1', hardcover_book_id: '42' };
    const dryRunApp = editionApp();
    dryRunApp.openSummary.runContext.dryRun = true;
    assert.match(dryRunApp.renderEditionActions(fallback), /data-edition-action="add" disabled/);
    assert.equal(editionApp({ isViewer: () => true }).renderEditionActions(fallback), '');
});

for (const match_method of ['edition_asin', 'isbn']) {
    test(`${match_method} audiobook fallback opens Audible import preview without replacing its original sync record`, async () => {
        const app = editionApp();
        const record = {
            book_id: 'li_fallback', outcome: 'skipped', reason: 'original reason', match_method,
            title: 'Fallback audiobook', format: 'audiobook', source_asin: 'B00SOURCE1', asin: 'B00OTHER01',
            hardcover_book_id: '42'
        };
        app.openSummary.records.set(record.book_id, record);
        const requests = [];
        app.fetchJsonWithTimeout = async url => {
            requests.push(url);
            if (url.endsWith('/edition-capability')) return { response: { ok: true, status: 200 }, data: { success: true, data: {} } };
            return { response: { ok: true, status: 200 }, data: { success: true, data: confirmedAudibleDraft({
                abs_item_id: record.book_id,
                source_identifiers: { asin: record.source_asin },
                audible_identifier_candidate: { asin: record.source_asin, region: 'us', correction_allowed: true },
                confirmed_region: 'us'
            }) } };
        };

        await app.openEditionDialog(record.book_id);

        assert.equal(app.editionDialog.audibleImport, true);
        assert.equal(app.editionDialog.record.outcome, 'skipped');
        assert.equal(app.editionDialog.record.reason, 'original reason');
        assert.equal(app.editionDialog.record.hardcover_book_id, '42');
        assert.ok(requests.includes(`/api/profiles/p1/edition-drafts/source/${record.book_id}`));
        assert.match(app.renderEditionDialog(app.editionDialog), /Audiobookshelf and Audnexus\/Audible comparison/);
        assert.match(app.renderEditionDialog(app.editionDialog), /Audible import may select a different Hardcover book/);
        assert.match(app.renderEditionDialog(app.editionDialog), /Reading progress and history already saved on the previous Hardcover book will remain there/);
        assert.equal(app.isValidEditionCreateResult(validCreateResult({ abs_item_id: record.book_id, hardcover_book_id: '99' }), record.book_id, 'audiobook', '42', app.isAudibleImportRecord(record)), true,
            'the Audible resolver may return a different Hardcover book from the original target');
    });
}

for (const match_method of ['edition_asin', 'isbn']) {
    test(`${match_method} audiobook fallback reopens its saved Audible import recovery against the original target`, async () => {
        const app = editionApp();
        const record = {
            book_id: 'li_fallback', outcome: 'synced', reason: 'original reason', match_method,
            title: 'Fallback audiobook', format: 'audiobook', source_asin: 'B00SOURCE1', hardcover_book_id: '42',
            edition_action: {
                outcome: 'unconfirmed',
                submitted_body: { run_id: 'run-1', abs_item_id: 'li_fallback', audible_identifier: 'B00SOURCE1:us' },
                data: { recovery_token: 'opaque-token', hardcover_book_id: '99', audible_identifier: 'B00SOURCE1:us' }
            }
        };
        app.openSummary.records.set(record.book_id, record);

        assert.match(app.renderEditionActions(record), /Resolve pending edition request/);
        await app.openEditionDialog(record.book_id);

        assert.equal(app.editionDialog.outcome, 'unconfirmed');
        assert.equal(app.editionDialog.audibleImport, true);
        assert.equal(app.editionDialog.recovery.recoveryToken, 'opaque-token');
        assert.equal(app.editionDialog.record.outcome, 'synced');
        assert.equal(app.editionDialog.record.hardcover_book_id, '42');
    });
}

test('Audible comparison hides empty subtitle and series groups but keeps populated groups paired', () => {
    const app = editionApp();
    const base = confirmedAudibleDraft();
    const empty = {
        source_metadata_preview: { ...base.metadata_preview, subtitle: '', series: '', series_position: '' },
        audnexus_record: { ...base.audnexus_record, subtitle: '', series: [], series_position: '' }
    };
    const emptyRows = app.audibleImportComparisonRows({ ...base, ...empty });
    assert.doesNotMatch(emptyRows, /data-comparison="subtitle"|data-comparison="series"|data-comparison="series_position"/);

    for (const [label, source_metadata_preview, audnexus_record] of [
        ['ABS subtitle', { ...empty.source_metadata_preview, subtitle: 'Subtitle' }, empty.audnexus_record],
        ['Audnexus subtitle', empty.source_metadata_preview, { ...empty.audnexus_record, subtitle: 'Subtitle' }]
    ]) {
        const rows = app.audibleImportComparisonRows({ ...base, source_metadata_preview, audnexus_record });
        assert.match(rows, /data-comparison="subtitle"/, label);
    }

    const populatedSeriesCases = [
        { source_metadata_preview: { ...empty.source_metadata_preview, series: 'Foundation' } },
        { source_metadata_preview: { ...empty.source_metadata_preview, series_position: 0 } },
        { audnexus_record: { ...empty.audnexus_record, series: ['Foundation'] } },
        { audnexus_record: { ...empty.audnexus_record, series_position: 0 } }
    ];
    for (const overrides of populatedSeriesCases) {
        const rows = app.audibleImportComparisonRows({ ...base, ...empty, ...overrides });
        assert.match(rows, /data-comparison="series"/);
        assert.match(rows, /data-comparison="series_position"/);
    }
});

test('Audible comparison uses original source dates and reflects the API precision verdict', () => {
    const app = editionApp();
    const base = confirmedAudibleDraft();
    const cases = [
        { sourceDate: '2014', audnexusDate: '2014-08-05', verdict: 'match', label: 'Match' },
        { sourceDate: '2014-08-04', audnexusDate: '2014-08-05', verdict: 'differs', label: 'Different' }
    ];

    for (const scenario of cases) {
        const draft = confirmedAudibleDraft({
            source_metadata_preview: { ...base.metadata_preview, release_date: scenario.sourceDate },
            metadata_preview: { ...base.metadata_preview, release_date: scenario.audnexusDate },
            audnexus_record: { ...base.audnexus_record, release_date: scenario.audnexusDate },
            audnexus_comparison: { ...base.audnexus_comparison, release_date: scenario.verdict }
        });
        const html = app.renderEditionDialog({
            mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
            loading: false, busy: false, runDryRun: false, capability: null, draft
        });
        const row = html.match(/<div\b(?=[^>]*\bdata-comparison="release_date")[^>]*>[\s\S]*?<\/div>/)?.[0] || '';
        assert.match(row, new RegExp(`Audiobookshelf<\\/span>${scenario.sourceDate}`));
        assert.match(row, new RegExp(`Audnexus/Audible<\\/span>${scenario.audnexusDate}`));
        assert.match(row, new RegExp(`>${scenario.label}<`));
    }
});

test('unknown, unavailable, and dry-run Audible previews cannot submit', () => {
    const app = editionApp();
    const base = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
        loading: false, busy: false, runDryRun: false, capability: null
    };
    for (const region_status of ['unknown', 'temporarily_unavailable']) {
        const warning = region_status === 'temporarily_unavailable'
            ? [{ code: 'audnex_temporarily_unavailable', message: 'Audnexus lookup is temporarily unavailable. Retry to check the regional Audible identifier.' }]
            : [];
        const html = app.renderEditionDialog({ ...base, draft: confirmedAudibleDraft({ region_status, confirmed_region: '', audnexus_record: undefined, warnings: warning }) });
        assert.match(html, /confirm-create" disabled/);
        assert.match(html, /data-edition-dialog="retry"[^>]*>Retry preview/);
        assert.match(html, /class="edition-error" role="alert"/);
        assert.doesNotMatch(html, /Refresh the preview|Retry to check/);
    }
    const unmatched = app.renderEditionDialog({
        ...base,
        draft: confirmedAudibleDraft({
            region_status: 'unknown', confirmed_region: '', audnexus_record: undefined,
            warnings: [{ code: 'language_defaults_to_english', message: 'Audiobookshelf language is shown for reference; the edition draft uses English.' }]
        })
    });
    assert.match(unmatched, /Audnexus could not confirm a matching audiobook\. Audible import is unavailable\./);
    assert.doesNotMatch(unmatched, /regional Audnexus record must be confirmed|Refresh the preview|edition draft uses English/);
    assert.match(unmatched, /class="edition-error" role="alert"/);
    assert.match(unmatched, /confirm-create" disabled/);
    const failedLookup = app.renderEditionDialog({
        ...base,
        draft: confirmedAudibleDraft({
            region_status: 'unknown', confirmed_region: '', audnexus_record: undefined,
            warnings: [{ code: 'audnex_lookup_failed', message: 'Audnexus did not confirm this regional Audible identifier.' }]
        })
    });
    assert.match(failedLookup, /Audnexus could not verify a regional match for this audiobook\. Audible import is unavailable\./);
    assert.doesNotMatch(failedLookup, /No matching audiobook was found|did not confirm this regional Audible identifier/);
    assert.match(app.renderEditionDialog({ ...base, runDryRun: true, draft: confirmedAudibleDraft() }), /confirm-create" disabled/);
});

test('retrying a failed Audnexus preview hides retry and enables Add edition after success', async () => {
    const app = editionApp();
    const dialog = app.editionDialog = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
        loading: false, busy: false, runDryRun: false, capability: null,
        audibleIdentifier: 'B00ABC1234:uk',
        draft: confirmedAudibleDraft({ region_status: 'temporarily_unavailable', confirmed_region: '', audnexus_record: undefined })
    };
    assert.match(app.renderEditionDialog(dialog), /data-edition-dialog="retry"[^>]*>Retry preview/);
    app.showEditionDialog = () => {};
    let creates = 0;
    app.fetchJsonWithTimeout = async () => { creates++; return {}; };
    await app.submitEditionCreate();
    assert.equal(creates, 0);
    const requests = [];
    app.fetchJsonWithTimeout = async (url, options) => {
        requests.push({ url, options });
        return { response: { ok: true, status: 200 }, data: { success: true, data:
            url.endsWith('/edition-capability') ? {} : confirmedAudibleDraft() } };
    };
    await app.loadEditionDraft();
    const html = app.renderEditionDialog(dialog);
    assert.doesNotMatch(html, /data-edition-dialog="retry"/);
    assert.doesNotMatch(html, /data-edition-dialog="confirm-create" disabled/);
    assert.match(html, /Audible identifier:<\/strong> B00ABC1234:uk/);
    assert.ok(requests.some(({ url }) => url.includes('audible_identifier=B00ABC1234%3Auk')));
    assert.ok(requests.every(({ options }) => !options.method || options.method === 'GET'));
});

test('capability gate blocks on known denial, permits unverified attempts, and passes when allowed', () => {
    const app = editionApp();
    const cap = {
        ebook: { status: 'allowed', can_attempt: true },
        audiobook: { status: 'denied', can_attempt: false, reason: 'no scope' }
    };
    assert.deepEqual(app.editionCapabilityGate(cap, 'audiobook', 'B00ABC1234'), { blocked: true, reason: 'no scope' });
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

test('audiobook dialog shows region status without identifier-edit controls', () => {
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
    assert.doesNotMatch(html, /name="audible_identifier(?:_preview)?"|Regional Audible identifier|ASIN:region|preview-audible/);
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
    const section = [...html.matchAll(/<details\b[^>]*>[\s\S]*?<\/details>/g)]
        .map(([details]) => details)
        .find(details => visibleText(details).includes('Audnexus details (helps to verify the match)')) || '';
    assert.ok(section, 'Audnexus preview should be available in an expandable details section');
    const text = visibleText(section);
    assert.match(text, /Audnex title/);
    assert.match(text, /Audnex author/);
    assert.match(text, /Audnex narrator/);
    assert.match(text, /2023-08-09/);
    assert.match(text, /Format type: Enhanced Audio/);
    assert.doesNotMatch(text, /ABS title|ABS author|ABS narrator|Abridged/);
});

test('an ISBN-only audiobook can be confirmed while malformed identifiers remain ineligible', () => {
    const app = editionApp();
    const dialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, loading: false, busy: false,
        capability: null,
        draft: {
            reading_format: 'audiobook', eligible: true, dry_run: false, region_status: 'unknown',
            source_identifiers: { asin: '', isbn: '9780306406157' }, metadata_preview: {
                title: '<ABS audio title>', author: 'ABS author', narrator: 'ABS narrator', publisher: 'Audio Publisher',
                language: 'en', release_date: '2024-01-02', edition_format: 'libro.fm',
                edition_information: 'Unabridged', audio_seconds: 3660, isbn_13: '9780306406157'
            },
            audible_identifier_candidate: { asin: '' }
        }
    };
    const html = app.renderEditionDialog(dialog);
    const text = visibleText(html);
    assert.match(html, /Audiobookshelf audiobook details/);
    assert.match(html, /&lt;ABS audio title&gt;/);
    assert.match(text, /Narrator: ABS narrator/);
    assert.match(text, /Publisher: Audio Publisher/);
    assert.match(text, /Audio length: 1 hr 1 min/);
    assert.match(text, /ISBN: 9780306406157/);
    assert.match(html, /data-edition-dialog="confirm-create"(?![^>]*disabled)/);
    assert.doesNotMatch(html, /<input[^>]+name="(?:title|narrator|release_date|edition_format)"/);

    const malformed = app.renderEditionDialog({
        ...dialog, draft: { ...dialog.draft, source_identifiers: { asin: 'bad', isbn: 'not-an-isbn' } }
    });
    assert.match(malformed, /valid ASIN or ISBN from Audiobookshelf/);
    assert.match(malformed, /no usable source ASIN or ISBN/);
    assert.match(malformed, /data-edition-dialog="confirm-create"[^>]*disabled/);

    const malformedASINWithISBN = app.renderEditionDialog({
        ...dialog, draft: { ...dialog.draft, source_identifiers: { asin: 'bad', isbn: '0-306-40615-2' } }
    });
    assert.doesNotMatch(malformedASINWithISBN, /valid ASIN or ISBN from Audiobookshelf/);
    assert.match(malformedASINWithISBN, /will be added using its Audiobookshelf ISBN/);

    const validUnknownRegion = app.renderEditionDialog({
        ...dialog,
        draft: { ...dialog.draft, region_status: 'unknown', source_identifiers: { asin: 'B00ABC1234', isbn: '9780306406157' } }
    });
    assert.match(validUnknownRegion, /app will retry region discovery during creation/);
    assert.doesNotMatch(validUnknownRegion, /will be added using its Audiobookshelf ISBN/);
    assert.doesNotMatch(validUnknownRegion, /Audiobookshelf audiobook details/);
    assert.match(validUnknownRegion, /data-edition-dialog="confirm-create"(?![^>]*disabled)/);
});

test('region discovery is automatic and the Audible identifier has no edit control', () => {
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
    assert.doesNotMatch(confirmed, /name="audible_identifier(?:_preview)?"|Regional Audible identifier|ASIN:region|preview-audible/);
    assert.match(confirmed, /Audible region confirmed automatically/);
    const unknown = app.renderEditionDialog({ ...base, draft: { ...base.draft, region_status: 'unknown' } });
    assert.doesNotMatch(unknown, /name="audible_identifier(?:_preview)?"|Regional Audible identifier|ASIN:region|preview-audible/);
    assert.match(unknown, /retry region discovery during creation/);
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
        ['', '<Dune>', '', '<Dune>'],
        ['', '&lt;Dune&gt;', '', '&lt;Dune&gt;']
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
        const text = visibleText(html);
        if (isbn.trim()) assert.ok(text.includes(`ISBN: ${isbn}`));
        else assert.doesNotMatch(text, /ISBN:/);
        if (expectedSeries) assert.ok(text.includes(`Series: ${expectedSeries}`));
        else assert.doesNotMatch(text, /Series:/);
    }
});

test('capability denial codes are translated to plain-language text', () => {
    const app = editionApp();
    assert.match(
        app.editionCapabilityGate({ audiobook: { status: 'denied', can_attempt: false, reason: 'hardcover_token_missing' } }, 'audiobook', 'B00ABC1234').reason,
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
        draft: { reading_format: 'audiobook', eligible: true, region_status: 'confirmed', confirmed_region: 'us', source_identifiers: { asin: 'B00ABC1234' }, audible_identifier_candidate: { asin: 'B00ABC1234' } }
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
        draft: { reading_format: 'audiobook', region_status: 'confirmed', confirmed_region: 'uk', source_identifiers: { asin: 'B00ABC1234' }, audible_identifier_candidate: { asin: 'B00ABC1234' } }
    };
    assert.deepEqual(app.buildEditionCreateBody(confirmedAudio, {}, false), {
        run_id: 'run-1', abs_item_id: 'li_9', audible_identifier: 'B00ABC1234:uk'
    });

    const isbnAudio = {
        runId: 'run-1', record: { book_id: 'li_9', asin: 'B00ABC1234' },
        draft: {
            reading_format: 'audiobook', region_status: 'confirmed', confirmed_region: 'uk',
            source_identifiers: { asin: '', isbn: '9780306406157' },
            audible_identifier_candidate: { asin: 'B00ABC1234' }
        }
    };
    assert.deepEqual(app.buildEditionCreateBody(isbnAudio, {}, false), {
        run_id: 'run-1', abs_item_id: 'li_9'
    });

    const unconfirmedAudio = {
        runId: 'run-1', record: { book_id: 'li_9' },
        draft: { reading_format: 'audiobook', region_status: 'unknown', source_identifiers: { asin: 'B00ABC1234', isbn: '9780306406157' } }
    };
    assert.deepEqual(app.buildEditionCreateBody(unconfirmedAudio, {}, false), {
        run_id: 'run-1', abs_item_id: 'li_9'
    });
});

test('unanchored Audible create confirms the reviewed identifier and accepts the resolved Hardcover identity', async () => {
    const app = editionApp();
    const record = { ...audibleImportRecord };
    const dialog = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record,
        loading: false, busy: false, error: '', result: null, runDryRun: false,
        draft: confirmedAudibleDraft()
    };
    app.editionDialog = dialog;
    app.readEditionFormFields = () => ({});
    app.showEditionDialog = () => {};
    app.fetchJsonWithTimeout = async (_url, options) => {
        const body = JSON.parse(options.body);
        assert.deepEqual(body, {
            run_id: 'run-1', abs_item_id: 'li_asin', audible_identifier: 'B00ABC1234:uk',
            audnexus_confirmed: true, resync: true
        });
        return { response: { ok: true, status: 200 }, data: { success: true, data: {
            abs_item_id: 'li_asin', reading_format: 'audiobook', status: 'loaded',
            hardcover_book_id: '71', hardcover_edition_id: '99', hardcover_title: '<b>Imported book</b>'
        } } };
    };
    await app.submitEditionCreate();
    assert.equal(dialog.result.hardcover_book_id, '71');
    const html = app.renderCreateResult(dialog.result);
    assert.match(html, /&lt;b&gt;Imported book&lt;\/b&gt;/);
    assert.doesNotMatch(html, /<b>Imported book/);
    assert.match(html, /Hardcover book ID:<\/strong> 71/);
    assert.match(html, /Edition ID:<\/strong> 99/);
});

test('Audible import uses regional import permission independently of insertion permission', async () => {
    for (const canImport of [true, false]) {
        const app = editionApp();
        const dialog = {
            mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
            loading: false, busy: false, error: '', result: null, runDryRun: false,
            draft: confirmedAudibleDraft(),
            capability: {
                audiobook: { status: canImport ? 'allowed' : 'denied', can_attempt: canImport },
                audiobook_isbn: { status: canImport ? 'denied' : 'allowed', can_attempt: !canImport },
                ebook: { status: canImport ? 'denied' : 'allowed', can_attempt: !canImport }
            }
        };
        app.editionDialog = dialog;
        app.readEditionFormFields = () => ({});
        app.showEditionDialog = () => {};
        let creates = 0;
        app.fetchJsonWithTimeout = async () => {
            creates++;
            return { response: { ok: true, status: 200 }, data: { success: true, data: {
                abs_item_id: audibleImportRecord.book_id, reading_format: 'audiobook', status: 'loaded',
                hardcover_book_id: '71', hardcover_edition_id: '99'
            } } };
        };
        const html = app.renderEditionDialog(dialog);
        assert.equal(/data-edition-dialog="confirm-create" disabled/.test(html), !canImport);
        assert.match(html, /Audible identifier:<\/strong> B00ABC1234:uk/);
        assert.doesNotMatch(html, /<input|<select|data-edition-dialog="confirm-identifier"/);
        await app.submitEditionCreate();
        assert.equal(creates, canImport ? 1 : 0);
    }
});

test('dry-run Audible modal blocks submission at the UI boundary', async () => {
    const app = editionApp();
    const dialog = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
        loading: false, busy: false, runDryRun: true, draft: confirmedAudibleDraft()
    };
    app.editionDialog = dialog;
    let creates = 0;
    app.fetchJsonWithTimeout = async () => { creates++; return {}; };
    await app.submitEditionCreate();
    assert.equal(creates, 0);
    assert.match(app.renderEditionDialog(dialog), /Dry run/);
    assert.match(app.renderEditionDialog(dialog), /confirm-create" disabled/);
});

test('ambiguous unanchored create errors offer recovery without another import button', async () => {
    const app = editionApp();
    const dialog = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
        loading: false, busy: false, error: '', result: null, runDryRun: false, draft: confirmedAudibleDraft()
    };
    app.editionDialog = dialog;
    app.readEditionFormFields = () => ({});
    app.showEditionDialog = () => {};
    app.fetchJsonWithTimeout = async () => ({ response: { ok: false, status: 503 }, data: { success: false, error: 'proxy timed out' } });
    await app.submitEditionCreate();
    assert.equal(dialog.outcome, 'transport_unknown');
    assert.ok(app.loadPendingEditionRecovery('p1', 'run-1', 'li_asin'));
    const html = app.renderEditionDialog(dialog);
    assert.match(html, /import result is unknown/);
    assert.doesNotMatch(html, /data-edition-dialog="(confirm-create|retry-create)"/);
});

test('create and recovery POSTs wait for Retry-After before allowing another request', async t => {
    const scenarios = [
        { name: 'unanchored create', record: audibleImportRecord, method: 'submitEditionCreate', button: 'retry-create' },
        { name: 'selected-book create', record: needsReview, method: 'submitEditionCreate', button: 'retry-create' },
        { name: 'ebook create preserves edits during cooldown', record: { ...needsReview, format: 'ebook' }, method: 'submitEditionCreate', button: 'retry-create', ebook: true },
        { name: 'unconfirmed recovery', record: audibleImportRecord, method: 'checkEditionImport', button: 'check-import', outcome: 'unconfirmed' },
        { name: 'unsaved-match recovery', record: audibleImportRecord, method: 'checkEditionImport', button: 'check-import', outcome: 'created' },
        { name: 'selected-book recovery', record: needsReview, method: 'checkEditionImport', button: 'check-import', outcome: 'created' }
    ];
    for (const scenario of scenarios) {
        await t.test(scenario.name, async t => {
            const app = editionApp();
            let now = Date.UTC(2026, 9, 5);
            let tick;
            const retryButton = { textContent: '', disabled: false };
            const titleInput = { name: 'title', value: 'Original title', dataset: { original: 'Original title' } };
            const previousDocument = global.document;
            global.document = {
                ...previousDocument,
                querySelector: () => ({ querySelectorAll: () => scenario.ebook ? [titleInput] : [] })
            };
            t.after(() => { global.document = previousDocument; });
            t.mock.method(Date, 'now', () => now);
            t.mock.method(global, 'setInterval', callback => { tick = callback; return 1; });
            t.mock.method(global, 'clearInterval', () => {});
            t.mock.method(global.document, 'getElementById', () => ({
                querySelectorAll: selector => selector.includes('data-edition-dialog') ? [retryButton] : []
            }));
            const dialog = app.editionDialog = {
                mode: 'create', profileId: 'p1', runId: 'run-1', record: scenario.record,
                loading: false, busy: false, retryAt: 0, outcome: scenario.outcome,
                draft: confirmedAudibleDraft(scenario.ebook ? {
                    reading_format: 'ebook', metadata_preview: { title: 'Original title' }
                } : {}),
                recovery: scenario.outcome ? {
                    runId: 'run-1', absItemId: scenario.record.book_id,
                    audibleIdentifier: 'B00ABC1234:uk', recoveryToken: 'saved-token'
                } : null
            };
            let html;
            app.showEditionDialog = () => { html = app.renderEditionDialog(dialog); };
            const requests = [];
            app.fetchJsonWithTimeout = async (url, options) => {
                requests.push({ url, body: JSON.parse(options.body) });
                return {
                    response: { ok: false, status: 429, headers: { get: () => scenario.outcome
                        ? new Date(now + 20000).toUTCString() : '20' } },
                    data: { success: false, outcome: 'not_submitted', error_code: 'edition_create_busy', error: 'Service busy' }
                };
            };
            const actionTag = () => html.match(new RegExp(`<button[^>]*data-edition-dialog="${scenario.button}"[^>]*>`))?.[0];
            await app[scenario.method]();
            assert.equal(requests.length, 1);
            assert.equal(dialog.retryAt, now + 20000);
            assert.match(actionTag(), / disabled/);
            assert.match(html, /Retry in 20s/);
            if (scenario.ebook) titleInput.value = 'Edited while waiting';
            await app[scenario.method]();
            assert.equal(requests.length, 1, 'direct method calls must also respect the cooldown');
            now += 19000;
            tick();
            assert.equal(retryButton.textContent, 'Retry in 1s');
            assert.equal(retryButton.disabled, true);
            await app[scenario.method]();
            assert.equal(requests.length, 1);
            now += 1000;
            tick();
            assert.doesNotMatch(actionTag(), / disabled/);
            assert.doesNotMatch(html, /Retry in \d+s/);
            assert.equal(dialog.timer, null);
            await app[scenario.method]();
            assert.equal(requests.length, 2);
            if (scenario.ebook) {
                assert.match(html, /value="Edited while waiting"/);
                assert.equal(requests[1].body.title, 'Edited while waiting');
            } else {
                assert.deepEqual(requests[1], requests[0], 'retry must preserve the submitted identifier and recovery token');
            }
            if (scenario.outcome) {
                assert.equal(dialog.outcome, scenario.outcome);
                assert.ok(requests.every(({ url }) => url.endsWith('/check-import')));
            }
        });
    }
});

test('server draft 429 holds the Audible preview action and restores its label after the wait', async t => {
    const app = editionApp();
    let now = 100000;
    let tick;
    const retryButton = { textContent: '', disabled: false };
    const originalNow = Date.now;
    const originalSetInterval = global.setInterval;
    const originalClearInterval = global.clearInterval;
    const previousDocument = global.document;
    Date.now = () => now;
    global.setInterval = callback => { tick = callback; return 1; };
    global.clearInterval = () => {};
    global.document = {
        ...previousDocument,
        getElementById: () => ({ querySelector: () => retryButton })
    };
    t.after(() => {
        Date.now = originalNow;
        global.setInterval = originalSetInterval;
        global.clearInterval = originalClearInterval;
        global.document = previousDocument;
    });
    app.profileUrl = () => '/profile';
    app.showEditionDialog = () => {};
    app.fetchJsonWithTimeout = async url => url.includes('edition-capability')
        ? { response: { ok: true, status: 200 }, data: { success: true, data: {} } }
        : { response: { ok: false, status: 429, headers: { get: () => '20' } }, data: { success: false, error: 'busy' } };
    const dialog = app.editionDialog = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
        audibleIdentifier: 'B00ABC1234:uk', loading: false, busy: false, error: '', retryAt: 0, draft: confirmedAudibleDraft()
    };
    await app.loadEditionDraft();
    assert.equal(dialog.draft, null);
    assert.ok(dialog.retryAt - Date.now() > 15000);
    const html = app.renderEditionDialog(dialog);
    assert.match(html, /Retry in \d+s/);
    assert.match(html, /data-edition-dialog="retry"[^>]*disabled/);
    now += 19000;
    tick();
    assert.equal(retryButton.textContent, 'Retry in 1s');
    assert.equal(retryButton.disabled, true);
    now += 1000;
    tick();
    assert.equal(retryButton.textContent, 'Retry preview');
    assert.equal(retryButton.disabled, false);
    assert.equal(dialog.timer, null);
});

test('unanchored pending import recovery accepts resolved IDs without changing its ABS and run identity', async () => {
    const app = editionApp();
    app.openSummary.records.set(audibleImportRecord.book_id, audibleImportRecord);
    const dialog = {
        mode: 'create', audibleImport: true, profileId: 'p1', runId: 'run-1', record: audibleImportRecord,
        loading: false, busy: false, error: '', result: null, runDryRun: false,
        draft: confirmedAudibleDraft()
    };
    app.editionDialog = dialog;
    app.readEditionFormFields = () => ({});
    app.showEditionDialog = () => {};
    let createBody;
    app.fetchJsonWithTimeout = async (_url, options) => {
        createBody = JSON.parse(options.body);
        return { response: { ok: false, status: 503 }, data: { success: false, outcome: 'unconfirmed', data: {
            audible_identifier: 'B00ABC1234:uk', recovery_token: 'opaque-token'
        } } };
    };
    await app.submitEditionCreate();
    assert.equal(dialog.outcome, 'unconfirmed');
    assert.equal(dialog.recovery.hardcoverBookId, '');
    const key = app.pendingEditionRecoveryKey('p1', 'run-1', 'li_asin');
    assert.ok(window.sessionStorage.getItem(key));
    assert.equal(createBody.abs_item_id, 'li_asin');
    assert.equal(createBody.run_id, 'run-1');

    app.editionDialog = null;
    await app.openEditionDialog('li_asin');
    const restoredDialog = app.editionDialog;
    assert.equal(restoredDialog.outcome, 'unconfirmed');
    assert.equal(restoredDialog.record.book_id, 'li_asin');
    assert.equal(restoredDialog.runId, 'run-1');
    assert.equal(restoredDialog.draft, null);
    assert.equal(restoredDialog.record.format, 'audiobook');
    assert.equal(restoredDialog.recovery.hardcoverBookId || '', '');
    const recoveryHTML = app.renderEditionDialog(restoredDialog);
    assert.match(recoveryHTML, /data-edition-dialog="check-import"/);
    assert.doesNotMatch(recoveryHTML, /data-edition-dialog="(?:confirm-create|retry-create)"/);

    let recoveryBody;
    let recoveryURL;
    app.fetchJsonWithTimeout = async (url, options) => {
        recoveryURL = url;
        recoveryBody = JSON.parse(options.body);
        return { response: { ok: true, status: 200 }, data: { success: true, data: {
            abs_item_id: 'li_asin', reading_format: 'audiobook', status: 'loaded',
            hardcover_book_id: '71', hardcover_edition_id: '99', hardcover_title: 'Recovered title'
        } } };
    };
    await app.checkEditionImport();
    assert.match(recoveryURL, /\/edition-drafts\/check-import$/);
    assert.deepEqual(recoveryBody, {
        run_id: 'run-1', abs_item_id: 'li_asin', audible_identifier: 'B00ABC1234:uk', recovery_token: 'opaque-token'
    });
    assert.equal(restoredDialog.result.reading_format, 'audiobook');
    assert.equal(restoredDialog.result.hardcover_book_id, '71');
    assert.equal(restoredDialog.result.hardcover_edition_id, '99');
    assert.equal(restoredDialog.result.hardcover_title, 'Recovered title');
    assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', 'li_asin'), null);
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
        draft: { reading_format: 'audiobook', region_status: 'confirmed', confirmed_region: 'us', source_identifiers: { asin: 'B00ABC1234' }, audible_identifier_candidate: { asin: 'B00ABC1234' }, dry_run: false }
    };
    await app.submitEditionCreate();
    assert.ok(capturedOptions.timeoutMs >= 65000, 'client timeout must cover the 65s server-side editionCreateRequestTimeout');
});

function stubDialog(app, status, payload) {
    app.fetchJsonWithTimeout = async () => ({ response: { ok: status < 300, status, headers: { get: () => null } }, data: payload });
    app.readEditionFormFields = () => ({});
    app.editionDialog = {
        mode: 'create', profileId: 'p1', runId: 'run-1', record: needsReview, busy: false, error: '', result: null,
        draft: { reading_format: 'audiobook', region_status: 'confirmed', confirmed_region: 'us', source_identifiers: { asin: 'B00ABC1234' }, audible_identifier_candidate: { asin: 'B00ABC1234' }, dry_run: false }
    };
    return app.editionDialog;
}

test('successful create updates the book action immediately and shows a separate resync failure', async t => {
    const app = editionApp();
    app.openSummary.records.set(String(needsReview.book_id), needsReview);
    const button = { closest() { return { dataset: { bookId: needsReview.book_id } }; }, outerHTML: '', removeAttribute() {} };
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

test('known create denials replace the row pending marker so a reopened draft can safely retry', async () => {
    const app = editionApp({ authEnabled: true, currentUser: { id: 'user-7' } });
    const record = { ...needsReview };
    app.openSummary.records.set(String(record.book_id), record);
    const dialog = stubDialog(app, 403, { success: false, error: 'Insufficient permissions' });
    dialog.record = record;
    let creates = 0;
    app.fetchJsonWithTimeout = async () => {
        creates++;
        return creates === 1
            ? { response: { ok: false, status: 403 }, data: { success: false, error: 'Insufficient permissions' } }
            : { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } };
    };

    await app.submitEditionCreate();

    assert.equal(dialog.outcome, 'not_submitted');
    const savedRecord = app.openSummary.records.get(String(record.book_id));
    assert.equal(savedRecord.edition_action.outcome, 'not_submitted');
    assert.match(app.renderEditionActions(savedRecord), /Retry add edition/);
    app.closeEditionDialog();
    app.loadEditionDraft = async () => {
        app.editionDialog.draft = { reading_format: 'audiobook', eligible: true, source_identifiers: {}, warnings: [] };
        app.editionDialog.loading = false;
    };
    await app.openEditionDialog(record.book_id);
    assert.equal(app.editionDialog.outcome, 'not_submitted');
    assert.equal(app.editionDialog.retryCreate, true);
    await app.submitEditionCreate();
    assert.equal(creates, 2);
    assert.ok(app.editionDialog.result);
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
    assert.match(refreshed.renderEditionActions({ ...needsReview, book_id: 'li_2', outcome: 'synced', match_method: 'edition_asin', source_asin: 'B00SOURCE1' }), /Hardcover Edition Added/);
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

test('edition creation relies on server persistence when browser session storage is unavailable', async () => {
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
            app.fetchJsonWithTimeout = async () => { creates++; return { response: { ok: false, status: 503 }, data: {
                success: false, outcome: 'unconfirmed', data: { audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'saved-token' }
            } }; };
            await app.submitEditionCreate();
            assert.equal(creates, 1, blocked.name);
            assert.equal(dialog.outcome, 'unconfirmed');
            assert.deepEqual(dialog.fieldValues, { title: 'Edited title' });
            assert.match(app.renderEditionDialog(dialog), /Check import status/);
            await app.submitEditionCreate();
            assert.equal(creates, 1);
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

test('details requests started before an accepted edition action cannot erase its added state', async t => {
    const previousDocument = global.document;
    const content = { contains: () => false };
    const container = { style: {} };
    global.document = {
        ...previousDocument,
        activeElement: null,
        getElementById(id) { return id === 'sync-summary-content' ? content : id === 'sync-summary-container' ? container : null; }
    };
    t.after(() => { global.document = previousDocument; });

    async function verifyAction(action) {
        const app = editionApp();
        app.openSummary.runId = 'run-1';
        app.openSummary.generation = 1;
        app.openSummary.editionActionRevision = 0;
        app.openSummary.addedEditionBookIds = new Set();
        app.openSummary.loading = false;
        app.openSummary.renderedRunId = 'run-1';
        app.statuses.p1 = { profile_name: 'Profile One', snapshot: { run_id: 'run-1' } };
        app.restoreDetailViewport = () => {};
        app.renderDetailsSnapshot = snapshot => {
            app.openSummary.addedEditionBookIds = new Set((snapshot.book_outcomes || [])
                .filter(record => record.edition_added === true).map(record => String(record.book_id)));
        };
        const oldDetails = deferred();
        app.fetchJsonWithTimeout = () => oldDetails.promise;
        const loading = app.fetchAndRenderDetails();
        await action(app);
        assert.ok(app.openSummary.addedEditionBookIds.has('li_1'));

        oldDetails.resolve({ response: { ok: true, status: 200 }, data: {
            run_id: 'run-1', state: 'completed', outcome_counts: {},
            book_outcomes: [{ ...needsReview, edition_added: false }]
        } });
        await loading;
        assert.ok(app.openSummary.addedEditionBookIds.has('li_1'), 'the earlier snapshot must not overwrite accepted local state');

        app.fetchJsonWithTimeout = async () => ({ response: { ok: true, status: 200 }, data: {
            run_id: 'run-1', state: 'completed', outcome_counts: {},
            book_outcomes: [{ ...needsReview, edition_added: false }]
        } });
        await app.fetchAndRenderDetails();
        assert.equal(app.openSummary.addedEditionBookIds.has('li_1'), false, 'a later server snapshot remains authoritative');
    }

    await verifyAction(async app => {
        app.fetchJsonWithTimeout = async (_url, options) => options?.method === 'POST'
            ? { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } }
            : { response: { ok: true, status: 200 }, data: {} };
        app.readEditionFormFields = () => ({});
        app.saveAddedEditionBookId = () => {};
        app.clearPendingEditionRecovery = () => {};
        app.editionDialog = {
            mode: 'create', profileId: 'p1', runId: 'run-1', record: { ...needsReview },
            draft: { reading_format: 'audiobook', dry_run: false }, busy: false, error: ''
        };
        await app.submitEditionCreate();
    });

    await verifyAction(async app => {
        app.fetchJsonWithTimeout = async () => ({ response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } });
        app.saveAddedEditionBookId = () => {};
        app.editionDialog = {
            mode: 'create', profileId: 'p1', runId: 'run-1', record: { ...needsReview },
            draft: { reading_format: 'audiobook' }, outcome: 'unconfirmed', busy: false,
            recovery: { runId: 'run-1', absItemId: 'li_1', audibleIdentifier: 'B00ABC1234:us', recoveryToken: 'token', hardcoverBookId: '42' }
        };
        await app.checkEditionImport();
    });
});

test('earlier details cannot replace a pending, failed, or retryable create result', async t => {
    const previousDocument = global.document;
    global.document = {
        ...previousDocument, activeElement: null,
        getElementById(id) { return id === 'sync-summary-container' ? { style: {} } : null; }
    };
    t.after(() => { global.document = previousDocument; });

    for (const outcome of ['unconfirmed', 'created', 'failed', 'not_submitted']) {
        const app = editionApp();
        const open = app.openSummary;
        Object.assign(open, { runId: 'run-1', generation: 1, renderedRunId: 'run-1', loading: false });
        open.records.set(String(needsReview.book_id), { ...needsReview });
        app.statuses.p1 = { snapshot: { run_id: 'run-1' } };
        app.restoreDetailViewport = () => {};
        app.renderDetailsSnapshot = snapshot => {
            open.records = new Map(snapshot.book_outcomes.map(record => [String(record.book_id), record]));
        };
        const snapshot = { run_id: 'run-1', book_outcomes: [{ ...needsReview, edition_action: {
            outcome: 'not_submitted', error: 'Earlier response',
            submitted_body: { run_id: 'run-1', abs_item_id: needsReview.book_id }
        } }] };
        const oldDetails = deferred();
        app.fetchJsonWithTimeout = () => oldDetails.promise;
        const loading = app.fetchAndRenderDetails();
        const payload = {
            success: false, outcome, error: 'Current response',
            data: { recovery_token: 'current-token', audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42' }
        };
        stubDialog(app, 503, payload);
        await app.submitEditionCreate();
        const accepted = app.editionRequestState(open.records.get(String(needsReview.book_id)), open);
        assert.equal(accepted.outcome, outcome);
        oldDetails.resolve({ response: { ok: true, status: 200 }, data: snapshot });
        await loading;
        assert.deepEqual(app.editionRequestState(open.records.get(String(needsReview.book_id)), open), accepted,
            `earlier details must preserve the ${outcome} result and its recovery data`);

        app.fetchJsonWithTimeout = async () => ({ response: { ok: true, status: 200 }, data: snapshot });
        await app.fetchAndRenderDetails();
        assert.equal(open.records.get(String(needsReview.book_id)).edition_action.error, 'Earlier response',
            'a details request started after the action remains authoritative');
    }
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
    assert.doesNotMatch(app.renderEditionDialog(dialog), /data-edition-dialog="confirm-create"/);
    assert.doesNotMatch(app.renderEditionDialog(dialog), /data-edition-dialog="check-import"/);
});

test('an import resolved to a non-audiobook edition is final and links to report that edition', async () => {
    const wrongFormat = {
        success: false, outcome: 'failed', error_code: 'hardcover_edition_wrong_format',
        error: 'Hardcover returned existing edition 32307716, which Hardcover lists as a physical book, not an audiobook.',
        data: { hardcover_book_id: '42', hardcover_edition_id: '32307716', hardcover_edition_url: 'https://evil.example/phish' }
    };
    const requireFinal = (app, dialog) => {
        const html = app.renderEditionDialog(dialog);
        assert.equal(dialog.outcome, 'failed');
        assert.match(html, /Hardcover returned a non-audiobook edition/);
        assert.match(html, /existing edition 32307716, which Hardcover lists as a physical book/);
        const reportLink = html.match(/<a\b(?=[^>]*href="https:\/\/hardcover\.app\/editions\/32307716")[^>]*>([\s\S]*?)<\/a>/)?.[1] || '';
        assert.match(visibleText(reportLink), /Report a problem on Hardcover/);
        assert.doesNotMatch(html, /may be stale/);
        assert.doesNotMatch(html, /evil\.example/);
        assert.match(html, /HTTP 409.*hardcover_edition_wrong_format/);
        assert.doesNotMatch(html, /data-edition-dialog="(check-import|confirm-create|retry-create)"/);
        assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
    };

    const createApp = editionApp();
    const createDialog = stubDialog(createApp, 409, wrongFormat);
    await createApp.submitEditionCreate();
    requireFinal(createApp, createDialog);

    const isbnApp = editionApp();
    const isbnDialog = stubDialog(isbnApp, 409, wrongFormat);
    isbnDialog.draft = {
        reading_format: 'audiobook', eligible: true, dry_run: false, region_status: 'not_applicable',
        source_identifiers: { asin: '', isbn: '9780306406157' }, audible_identifier_candidate: { asin: '' }
    };
    let submittedBody;
    isbnApp.fetchJsonWithTimeout = async (_url, options) => {
        submittedBody = JSON.parse(options.body);
        return { response: { ok: false, status: 409 }, data: wrongFormat };
    };
    await isbnApp.submitEditionCreate();
    requireFinal(isbnApp, isbnDialog);
    assert.equal(submittedBody.audible_identifier, undefined);
    assert.equal(submittedBody.resync, true);

    const checkApp = editionApp();
    const checkDialog = stubDialog(checkApp, 503, { success: false, outcome: 'unconfirmed', error_code: 'hardcover_import_unconfirmed', data: {
        audible_identifier: '0593396960:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
    } });
    await checkApp.submitEditionCreate();
    checkApp.fetchJsonWithTimeout = async () => ({ response: { ok: false, status: 409 }, data: wrongFormat });
    await checkApp.checkEditionImport();
    requireFinal(checkApp, checkDialog);
});

test('missing Audible mappings produce final report guidance for create and recovery', async t => {
    const missingMapping = {
        success: false, outcome: 'failed', error_code: 'hardcover_audible_mapping_missing',
        error: 'Hardcover returned edition 123 without a confirmed regional Audible mapping. The match was not saved.',
        data: { hardcover_book_id: '42', hardcover_edition_id: '123', hardcover_edition_url: 'https://evil.example/phish' }
    };
    for (const recovering of [false, true]) {
        await t.test(recovering ? 'recovery check' : 'create', async () => {
            const app = editionApp();
            const dialog = stubDialog(app, recovering ? 503 : 409, recovering
                ? { success: false, outcome: 'unconfirmed', data: {
                    audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42', recovery_token: 'opaque-token'
                } }
                : missingMapping);
            await app.submitEditionCreate();
            if (recovering) {
                app.fetchJsonWithTimeout = async () => ({ response: { ok: false, status: 409 }, data: missingMapping });
                await app.checkEditionImport();
            }
            const html = app.renderEditionDialog(dialog);
            const text = visibleText(html);
            assert.equal(dialog.outcome, 'failed');
            assert.match(text, /missing its Audible mapping/);
            assert.match(text, /edition 123.*match was not saved/);
            assert.match(text, /use Report to ask for the regional Audible identifier to be linked/);
            assert.match(text, /Run a new sync after Hardcover corrects the catalogue/);
            assert.match(html, /href="https:\/\/hardcover\.app\/editions\/123"/);
            assert.doesNotMatch(html, /evil\.example/);
            assert.doesNotMatch(text, /check again later|may be stale|may still be processing/i);
            assert.doesNotMatch(html, /data-edition-dialog="(?:check-import|confirm-create|retry-create)"/);
            assert.equal(app.loadPendingEditionRecovery('p1', 'run-1', 'li_1'), null);
        });
    }
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

test('server-confirmed additions render on a different browser without local storage', async t => {
    const previousStorage = window.localStorage;
    window.localStorage = { getItem() { return null; }, setItem() {} };
    t.after(() => { window.localStorage = previousStorage; });
    const app = editionApp();
    const record = { ...needsReview, edition_added: true };
    app.openSummary.addedEditionBookIds = new Set();
    app.openSummary.records = new Map([[String(record.book_id), record]]);
    app.loadPendingEditionRecovery = () => ({ outcome: 'transport_unknown' });
    assert.match(app.renderEditionActions(record), /Hardcover Edition Added/);
    assert.doesNotMatch(app.renderEditionActions(record), /data-edition-action/);
    app.editionDialog = null;
    await app.openEditionDialog(record.book_id);
    assert.equal(app.editionDialog, null);
    assert.match(app.renderEditionActions({ ...record, edition_added: false }), /Resolve pending edition request/);
});

test('fresh server details replace stale browser markers after a match is forgotten', t => {
    const app = editionApp();
    app.statuses = { p1: { profile_name: 'Profile One' } };
    app.openSummary.addedEditionBookIds = new Set([String(needsReview.book_id)]);
    const content = { innerHTML: '', querySelector: () => null, querySelectorAll: () => [] };
    const tabs = { innerHTML: '' };
    const previousDocument = global.document;
    global.document = {
        ...previousDocument,
        getElementById(id) { return id === 'sync-summary-content' ? content : id === 'sync-summary-tabs' ? tabs : null; }
    };
    t.after(() => { global.document = previousDocument; });
    const snapshot = { run_id: 'run-1', state: 'completed', outcome_counts: { needs_review: 1 }, book_outcomes: [{ ...needsReview, edition_added: true }] };
    app.renderDetailsSnapshot(snapshot);
    assert.match(content.innerHTML, /Hardcover Edition Added/);
    snapshot.book_outcomes = [{ ...needsReview }];
    app.renderDetailsSnapshot(snapshot);
    assert.doesNotMatch(content.innerHTML, /Hardcover Edition Added/);
    assert.match(content.innerHTML, /data-edition-action="add"/);
});

test('server edition request states restore in a browser without session recovery', async t => {
    const previousStorage = window.sessionStorage;
    window.sessionStorage = null;
    t.after(() => { window.sessionStorage = previousStorage; });
    const cases = [
        { outcome: 'unconfirmed', label: 'Resolve pending edition request', dialog: /Check import status/, token: 'saved-token' },
        { outcome: 'created', label: 'Resolve pending edition request', dialog: /Save match/, token: 'saved-token' },
        { outcome: 'transport_unknown', label: 'Resolve pending edition request', dialog: /import result is unknown/ },
        { outcome: 'unconfirmed', label: 'Resolve pending edition request', dialog: /cannot check this import safely/ },
        { outcome: 'failed', label: 'Review edition request', dialog: /edition request failed/ },
        { outcome: 'failed', label: 'Review edition request', dialog: /Report a problem on Hardcover/,
            error_code: 'hardcover_edition_wrong_format', edition: '123' },
        { outcome: 'failed', label: 'Review edition request', dialog: /missing its Audible mapping/,
            error_code: 'hardcover_audible_mapping_missing', edition: '456' }
    ];
    for (const scenario of cases) {
        const app = editionApp();
        const record = { ...needsReview, edition_action: {
            outcome: scenario.outcome, error_code: scenario.error_code || '', http_status: 409,
            submitted_body: { run_id: 'run-1', abs_item_id: needsReview.book_id, audible_identifier: 'B00ABC1234:us' },
            data: { recovery_token: scenario.token, hardcover_book_id: '42', hardcover_edition_id: scenario.edition,
                audible_identifier: 'B00ABC1234:us' }
        } };
        app.openSummary.records.set(record.book_id, record);
        let requests = 0;
        app.fetchJsonWithTimeout = async () => { requests++; throw new Error('must not send a create'); };
        assert.match(app.renderEditionActions(record), new RegExp(scenario.label));
        await app.openEditionDialog(record.book_id);
        assert.match(app.renderEditionDialog(app.editionDialog), scenario.dialog);
        await app.submitEditionCreate();
        assert.equal(requests, 0, scenario.outcome);
        if (!scenario.token) assert.doesNotMatch(app.renderEditionDialog(app.editionDialog), /data-edition-dialog="check-import"/);
    }
});

test('saved not-submitted request refreshes its draft and retains edited fields for safe retry', async () => {
    const app = editionApp();
    const record = { ...needsReview, format: 'ebook', edition_action: {
        outcome: 'not_submitted', error: 'Nothing was submitted', http_status: 503,
        submitted_body: { run_id: 'run-1', abs_item_id: needsReview.book_id, title: 'Corrected title', isbn_10: '' }
    } };
    app.openSummary.records.set(record.book_id, record);
    let previews = 0;
    app.loadEditionDraft = async () => {
        previews++;
        app.editionDialog.draft = { reading_format: 'ebook', eligible: true, source_identifiers: {}, warnings: [] };
        app.editionDialog.loading = false;
    };
    assert.match(app.renderEditionActions(record), /Retry add edition/);
    await app.openEditionDialog(record.book_id);
    assert.equal(previews, 1);
    assert.equal(app.editionDialog.retryCreate, true);
    assert.equal(app.editionDialog.error, 'Nothing was submitted');
    assert.deepEqual(app.editionDialog.fieldValues, { title: 'Corrected title', isbn_10: '' });
});

test('restored not-submitted Audible identifier is displayed read-only and freshly previewed before retry', async () => {
    const app = editionApp();
    const correctedIdentifier = 'B0OTHER123:ca';
    const record = { ...audibleImportRecord, edition_action: {
        outcome: 'not_submitted', error: 'Nothing was submitted', http_status: 503,
        submitted_body: {
            run_id: 'run-1', abs_item_id: audibleImportRecord.book_id,
            audible_identifier: correctedIdentifier, audnexus_confirmed: true
        }
    } };
    app.openSummary.records.set(record.book_id, record);
    const requests = [];
    app.fetchJsonWithTimeout = async (url, options = {}) => {
        requests.push({ url, options });
        if (url.endsWith('/edition-capability')) {
            return { response: { ok: true, status: 200 }, data: { success: true, data: {} } };
        }
        const requestedIdentifier = decodeURIComponent(url.split('audible_identifier=')[1] || '');
        const identifier = requestedIdentifier || 'B00ABC1234:us';
        const [asin, region] = identifier.split(':');
        const baseDraft = confirmedAudibleDraft();
        return { response: { ok: true, status: 200 }, data: { success: true, data: {
            ...baseDraft,
            confirmed_region: region,
            audible_identifier_candidate: { asin, region, correction_allowed: true },
            audnexus_record: { ...baseDraft.audnexus_record, asin }
        } } };
    };

    await app.openEditionDialog(record.book_id);
    const dialog = app.editionDialog;
    const previewRequest = requests.find(request => request.url.includes('/edition-drafts/source/'));
    assert.equal(previewRequest.url, `/api/profiles/p1/edition-drafts/source/${record.book_id}?audible_identifier=B0OTHER123%3Aca`);
    assert.equal(dialog.runId, 'run-1');
    assert.equal(dialog.record.book_id, record.book_id);
    assert.equal(dialog.audibleIdentifier, correctedIdentifier);
    assert.equal(dialog.draft.source_identifiers.asin, audibleImportRecord.asin);
    assert.equal(dialog.draft.confirmed_region, 'ca');
    assert.equal(dialog.retryCreate, true);
    assert.equal(requests.some(request => request.url.endsWith('/edition-drafts/create')), false);
    assert.doesNotMatch(app.renderEditionDialog(dialog), /<input|<select|data-edition-dialog="confirm-identifier"/);
    assert.match(app.renderEditionDialog(dialog), /Audible identifier:<\/strong> B0OTHER123:ca/);

    let createBody;
    app.readEditionFormFields = () => ({});
    app.fetchJsonWithTimeout = async (_url, options) => {
        createBody = JSON.parse(options.body);
        return { response: { ok: false, status: 503 }, data: {
            success: false, outcome: 'not_submitted', error: 'still unavailable'
        } };
    };
    await app.submitEditionCreate();
    assert.equal(createBody.run_id, 'run-1');
    assert.equal(createBody.abs_item_id, record.book_id);
    assert.equal(createBody.audible_identifier, correctedIdentifier);
    assert.equal(createBody.audnexus_confirmed, true);
});

test('a new browser resolves a server-saved request through status checks without creating again', async () => {
    const app = editionApp();
    const record = { ...needsReview, edition_action: {
        outcome: 'unconfirmed', http_status: 503,
        submitted_body: { run_id: 'run-1', abs_item_id: needsReview.book_id, resync: true },
        data: { recovery_token: 'server-token', audible_identifier: 'B00ABC1234:us', hardcover_book_id: '42' }
    } };
    app.openSummary.records.set(record.book_id, record);
    const requests = [];
    app.fetchJsonWithTimeout = async (url, options) => {
        requests.push({ url, body: JSON.parse(options.body) });
        return { response: { ok: true, status: 200 }, data: { success: true, data: validCreateResult() } };
    };
    await app.openEditionDialog(record.book_id);
    await app.checkEditionImport();
    assert.deepEqual(requests, [{ url: '/api/profiles/p1/edition-drafts/check-import', body: {
        run_id: 'run-1', abs_item_id: needsReview.book_id, audible_identifier: 'B00ABC1234:us', recovery_token: 'server-token'
    } }]);
    assert.match(app.renderEditionActions(record), /Hardcover Edition Added/);
});

test('a saved edition request from a different run or item cannot change the action', () => {
    const app = editionApp();
    for (const body of [{ run_id: 'other-run', abs_item_id: needsReview.book_id }, { run_id: 'run-1', abs_item_id: 'other-item' }]) {
        const record = { ...needsReview, edition_action: { outcome: 'failed', submitted_body: body } };
        assert.match(app.renderEditionActions(record), />Add edition<\/button>/);
        assert.equal(app.editionRequestState(record, app.openSummary), null);
    }
});

test('profile edit keeps an explicit zero ownership interval and defaults legacy profiles to 30', () => {
    const previousDocument = global.document;
    const fields = new Map();
    global.document = {
        getElementById(id) {
            if (!fields.has(id)) fields.set(id, { style: {} });
            return fields.get(id);
        }
    };
    try {
        const app = createApp();
        for (const [value, expected] of [[undefined, 30], [0, 0], [7, 7]]) {
            app.currentEditUser = {
                profile: { id: 'owned', name: 'Owned' },
                audiobookshelf_url: 'https://abs.example',
                sync_config: { ownership_recheck_days: value }
            };
            app.showEditModal();
            assert.equal(fields.get('edit-ownership-recheck-days').value, expected);
        }
    } finally {
        global.document = previousDocument;
    }
});

test('profile create and update submit the selected ownership interval, including zero', async () => {
    const previousFormData = global.FormData;
    const previousFetch = global.fetch;
    global.FormData = class {
        constructor(values) { this.values = values; }
        get(name) { return this.values[name] ?? null; }
    };
    try {
        const app = createApp();
        app.beginSessionMutation = () => ({});
        app.isCurrentSessionMutation = () => true;
        app.finishSessionMutation = () => false;
        app.showLoading = app.showToast = app.closeEditModal = app.loadProfiles = app.showTab = () => {};
        for (const method of ['handleAddProfile', 'handleEditProfile']) {
            for (const days of [0, 7, 30]) {
                const requests = [];
                global.fetch = async (url, options) => {
                    requests.push(JSON.parse(options.body));
                    return { json: async () => ({ success: true }) };
                };
                await app[method]({ target: { id: 'owned', ownership_recheck_days: String(days), reset() {} } });
                const request = requests.find(body => body.sync_config);
                assert.equal(request.sync_config.ownership_recheck_days, days);
            }
        }
    } finally {
        global.FormData = previousFormData;
        global.fetch = previousFetch;
    }
});
