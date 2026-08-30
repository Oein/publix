// The dashboard's single point of contact with the server.
//
// Every call funnels through request() so that authentication failures,
// network errors and the server's structured error shape are handled once
// rather than at each of the several dozen call sites.

/** Raised for any non-2xx response, carrying the server's own explanation. */
export class ApiError extends Error {
  constructor(message, { status = 0, details = [] } = {}) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.details = details;
  }
}

/** Set by the app so a 401 anywhere can bounce straight to the login screen. */
let onUnauthorized = () => {};
export function setUnauthorizedHandler(fn) {
  onUnauthorized = fn;
}

async function request(method, path, body) {
  let res;
  try {
    res = await fetch(path, {
      method,
      headers: body === undefined ? {} : { 'Content-Type': 'application/json' },
      body: body === undefined ? undefined : JSON.stringify(body),
      credentials: 'same-origin',
    });
  } catch (err) {
    // A failed fetch means the server is unreachable, which is worth
    // saying plainly rather than surfacing "Failed to fetch".
    throw new ApiError('Cannot reach the publix server. Is it still running?', { status: 0 });
  }

  if (res.status === 401) {
    onUnauthorized();
    throw new ApiError('Your session has expired. Sign in again.', { status: 401 });
  }

  const text = await res.text();
  let payload = null;
  if (text) {
    try {
      payload = JSON.parse(text);
    } catch {
      payload = null;
    }
  }

  if (!res.ok) {
    const message = payload?.error || text || `${res.status} ${res.statusText}`;
    throw new ApiError(message, { status: res.status, details: payload?.details ?? [] });
  }
  return payload;
}

const get = (path) => request('GET', path);
const post = (path, body) => request('POST', path, body ?? {});
const put = (path, body) => request('PUT', path, body);
const patch = (path, body) => request('PATCH', path, body);
const del = (path) => request('DELETE', path);

export const api = {
  auth: {
    state: () => get('/api/auth'),
    setup: (password) => post('/api/auth/setup', { password }),
    login: (password) => post('/api/auth/login', { password }),
    logout: () => post('/api/auth/logout'),
    changePassword: (current, next) => post('/api/auth/password', { current, new: next }),
  },

  projects: {
    list: () => get('/api/projects'),
    get: (id) => get(`/api/projects/${id}`),
    create: (body) => post('/api/projects', body),
    update: (id, body) => patch(`/api/projects/${id}`, body),
    remove: (id) => del(`/api/projects/${id}`),
    deploy: (id, body) => post(`/api/projects/${id}/deploy`, body),
    rollback: (id, deployment) => post(`/api/projects/${id}/rollback`, { deployment }),
    rollbackPlan: (id, deployment) =>
      get(`/api/projects/${id}/rollback-plan?deployment=${encodeURIComponent(deployment ?? '')}`),
    cancel: (id) => post(`/api/projects/${id}/cancel`),
    deployment: (id, did) => get(`/api/projects/${id}/deployments/${did}`),
    buildLog: (id, did) => get(`/api/projects/${id}/deployments/${did}/logs`),
    containers: (id) => get(`/api/projects/${id}/containers`),
    setEnv: (id, env) => put(`/api/projects/${id}/env`, { env }),
    setDomains: (id, domains) => put(`/api/projects/${id}/domains`, { domains }),
    runCron: (id, job) => post(`/api/projects/${id}/cron/${encodeURIComponent(job)}/run`),
  },

  github: {
    status: () => get('/api/github'),
    connect: (body) => put('/api/github', body),
    disconnect: () => del('/api/github'),
    repos: (q) => get(`/api/github/repos${q ? `?q=${encodeURIComponent(q)}` : ''}`),
    inspect: (owner, repo, ref) =>
      get(`/api/github/repos/${owner}/${repo}/inspect${ref ? `?ref=${encodeURIComponent(ref)}` : ''}`),
    import: (body) => post('/api/github/import', body),
  },

  settings: {
    get: () => get('/api/settings'),
    update: (body) => put('/api/settings', body),
    addVolume: (body) => post('/api/volumes', body),
    removeVolume: (name) => del(`/api/volumes/${encodeURIComponent(name)}`),
  },

  system: () => get('/api/system'),
};

/**
 * Subscribes to a server-sent event stream.
 *
 * Returns a stop function. The browser's EventSource reconnects on its own,
 * which is exactly the behaviour wanted for a dashboard left open while the
 * server restarts.
 */
export function stream(path, handlers) {
  const source = new EventSource(path, { withCredentials: true });
  for (const [event, handler] of Object.entries(handlers)) {
    if (event === 'error') {
      source.onerror = handler;
    } else {
      source.addEventListener(event, (e) => {
        try {
          handler(JSON.parse(e.data));
        } catch {
          // A malformed frame is not worth tearing the stream down for.
        }
      });
    }
  }
  return () => source.close();
}

export const buildLogStream = (id, did, handlers) =>
  stream(`/api/projects/${id}/deployments/${did}/logs?follow=1`, handlers);

export const runtimeLogStream = (id, handlers, tail = 200) =>
  stream(`/api/projects/${id}/runtime-logs?follow=1&tail=${tail}`, handlers);

export const eventStream = (handlers) => stream('/api/events', handlers);
