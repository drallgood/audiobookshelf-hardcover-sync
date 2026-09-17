const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');
const vm = require('node:vm');

const { JSDOM } = require('jsdom');

const APP_SOURCE = fs.readFileSync(path.join(__dirname, 'app.js'), 'utf8');

function deferred() {
    let resolve;
    const promise = new Promise((resolvePromise) => {
        resolve = resolvePromise;
    });
    return { promise, resolve };
}

function response(data, status = 200) {
    return {
        ok: status >= 200 && status < 300,
        status,
        json: async () => data
    };
}

function statusReport({ runId, state, profileName = 'Library' }) {
    return {
        profile_id: 'p1',
        profile_name: profileName,
        status: state,
        dry_run: false,
        snapshot: runId
            ? {
                run_id: runId,
                state,
                run_started_at: '2026-09-16T20:00:00Z',
                books_total: 1,
                processed_so_far: state === 'completed' ? 1 : 0,
                outcome_counts: {}
            }
            : null
    };
}

function statusResult(status) {
    return {
        response: response({ success: true, data: [status] }),
        data: { success: true, data: [status] }
    };
}

function makeApp() {
    const dom = new JSDOM(`
        <div id="sync-status"></div>
        <div id="toast-container"></div>
        <div id="loading-overlay"></div>
    `, { url: 'https://example.test/' });
    dom.window.scrollTo = () => {};
    const addEventListener = dom.window.document.addEventListener.bind(dom.window.document);
    dom.window.document.addEventListener = (type, ...args) => {
        // The production bootstrap is intentionally not part of these tests;
        // the harness constructs the app prototype with deterministic state.
        if (type === 'DOMContentLoaded') return;
        return addEventListener(type, ...args);
    };

    const context = vm.createContext(dom.window);
    vm.runInContext(APP_SOURCE, context, { filename: 'app.js' });
    const app = vm.runInContext('Object.create(SyncProfileApp.prototype)', context);

    Object.assign(app, {
        users: [{ id: 'p1', name: 'Library' }],
        statuses: Object.create(null),
        actionErrors: new Map(),
        currentEditUser: null,
        refreshInterval: null,
        currentUser: null,
        authEnabled: false,
        hasRedirectedToLogin: false,
        autoRefreshEnabled: false,
        statusLoadSequence: 0,
        activeStatusLoads: 0,
        activeStatusRequests: 0,
        statusLoadController: null,
        profileLoadFailed: false,
        profileRetryFailures: 0,
        nextProfileRetryAt: 0,
        statusRefreshError: null,
        openSummary: null,
        statusRefreshQueued: false,
        statusRefreshWaiters: [],
        terminalErrorCache: new Map(),
        terminalErrorRetries: new Map(),
        terminalErrorRequests: new Map(),
        authSessionGeneration: 0,
        editProfileRequest: null,
        sessionMutationRequests: new Map(),
        trackedRunIds: new Map()
    });

    return { app, dom, context };
}

async function waitFor(predicate) {
    for (let attempt = 0; attempt < 100; attempt += 1) {
        if (predicate()) return;
        await new Promise(resolve => setImmediate(resolve));
    }
    assert.fail('Timed out waiting for the UI request state');
}

function badge(dom) {
    return dom.window.document.querySelector('.status-card .status-badge')?.textContent;
}

async function exerciseAcceptedStart(authoritativeStatus, expectedBadge, expectedRunId) {
    const { app, dom, context } = makeApp();
    app.statuses.p1 = statusReport({ runId: 'old-run', state: 'completed' });
    app.renderStatuses();

    const aggregateRequests = [];
    app.fetchJsonWithTimeout = () => {
        const request = deferred();
        aggregateRequests.push(request);
        return request.promise;
    };
    context.fetch = async (url, options = {}) => {
        assert.equal(url, '/api/profiles/p1/sync');
        assert.equal(options.method, 'POST');
        return response({
            success: true,
            data: {
                profile_id: 'p1',
                run_id: 'queued-run',
                state: 'queued',
                queued_at: '2026-09-16T20:01:00Z',
                run_started_at: '2026-09-16T20:01:00Z'
            }
        }, 202);
    };

    const initialLoad = app.loadStatuses();
    await waitFor(() => aggregateRequests.length === 1);
    const start = app.startSync('p1');
    await waitFor(() => app.trackedRunIds.get('p1')?.runId === 'queued-run');
    assert.equal(badge(dom), 'Queued');

    // This response was started before acceptance and still reports the old
    // terminal run. It must not repaint over the accepted queued run.
    aggregateRequests[0].resolve(statusResult(statusReport({ runId: 'old-run', state: 'completed' })));
    await initialLoad;
    await waitFor(() => aggregateRequests.length === 2);
    assert.equal(badge(dom), 'Queued');
    assert.match(dom.window.document.querySelector('.status-card').textContent, /Queued/);

    // The refresh started after acceptance is authoritative, even when the
    // server restored a different run or has no current run after restart.
    aggregateRequests[1].resolve(statusResult(authoritativeStatus));
    await start;
    assert.equal(app.trackedRunIds.has('p1'), false);
    assert.equal(badge(dom), expectedBadge);
    assert.equal(app.statuses.p1.snapshot?.run_id ?? null, expectedRunId);
    assert.equal(
        dom.window.document.querySelector('.status-card [data-profile-action="summary"]') !== null,
        expectedRunId !== null
    );

    dom.window.close();
}

test('late pre-acceptance status response cannot replace the queued run in the DOM', async () => {
    await exerciseAcceptedStart(
        statusReport({ runId: 'restored-run', state: 'running' }),
        'Running',
        'restored-run'
    );
});

test('post-acceptance no-current-run response is authoritative and clears the guard', async () => {
    await exerciseAcceptedStart(statusReport({ runId: '', state: 'idle' }), 'Idle', null);
});
