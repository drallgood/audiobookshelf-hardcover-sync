const assert = require('node:assert/strict');
const test = require('node:test');

global.window = {
    addEventListener() {},
    location: { pathname: '/' }
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
    assert.deepEqual(app.editionCapabilityGate(null, 'audiobook'), { blocked: false, warning: '' });

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
    assert.deepEqual(app.editionCapabilityGate(cap, 'ebook'), { blocked: false, warning: '' });
    const unverified = app.editionCapabilityGate({ ebook: { status: 'unverified', can_attempt: true, warning: 'unverified!' } }, 'ebook');
    assert.equal(unverified.blocked, false);
    assert.equal(unverified.warning, '');
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

test('failed ebook create redraw preserves scroll and escaped edits and retries the same request', async () => {
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
            return requests.length === 1
                ? { response: { ok: false, status: 500 }, data: { success: false, error: 'temporary failure' } }
                : { response: { ok: true, status: 200 }, data: { success: true, data: { status: 'created' } } };
        };
        await app.submitEditionCreate();
        assert.equal(content.body.scrollTop, 500);
        assert.match(content.html, /&lt;New &amp; title&gt;/);
        assert.match(content.html, /name="isbn_10" value="" data-original="old10"/);
        assert.deepEqual(requests[0], { run_id: 'run-1', abs_item_id: 'li_1', title: '<New & title>', isbn_10: '', isbn_13: '9780000000002', resync: true });
        await app.submitEditionCreate();
        assert.equal(content.body.scrollTop, 500);
        assert.deepEqual(requests[1], requests[0]);
    } finally {
        global.document = originalDocument;
    }
});

test('submitting an edition create uses a timeout long enough for the server\'s own budget', async () => {
    const app = editionApp();
    let capturedOptions;
    app.fetchJsonWithTimeout = async (_url, options) => {
        capturedOptions = options;
        return { response: { ok: true, status: 200 }, data: { success: true, data: { status: 'created' } } };
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
        status: 'created', hardcover_book_id: '42', hardcover_edition_id: '99',
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
    stubDialog(app, 200, { success: true, data: { status: 'created' } });
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
    assert.match(dialog.error, /Permission denied: Hardcover token is missing/);
    assert.equal(dialog.result, null);

    dialog = stubDialog(app, 409, { success: false, error: 'sync run no longer contains a usable needs-review source record' });
    await app.submitEditionCreate();
    assert.match(dialog.error, /no longer contains/);
    assert.match(dialog.error, / The record may be stale, or a sync started after this page loaded; refresh and try again\.$/);
    assert.match(app.renderEditionActions(needsReview), /data-edition-action="add"/);
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
