import http from 'k6/http';
import { check, sleep } from 'k6';
import { Counter, Rate, Trend } from 'k6/metrics';

const BASE_URL = (__ENV.BASE_URL || 'http://localhost:8080').replace(/\/$/, '');
const GROUP_ID = __ENV.GROUP_ID || '';
const TEST_EMAIL = __ENV.TEST_EMAIL || '';
const TEST_PASSWORD = __ENV.TEST_PASSWORD || '';
const VUS = parseInt(__ENV.K6_VUS || '1', 10);
const ITERATIONS = parseInt(__ENV.K6_ITERATIONS || '100', 10);

export const options = {
  vus: VUS,
  iterations: ITERATIONS,
};

const dashboardLatency = new Trend('dashboard_latency', true);
const dashboardRequests = new Counter('dashboard_requests');
const dashboardErrors = new Rate('dashboard_errors');
const cacheHit = new Counter('cache_status_hit');
const cacheMiss = new Counter('cache_status_miss');
const cacheBypass = new Counter('cache_status_bypass');
const cacheError = new Counter('cache_status_error');

let loggedIn = false;
let warmedUp = false;

function loginHeaders() {
  return {
    'Content-Type': 'application/json',
    Origin: __ENV.APP_ORIGIN || BASE_URL,
    'X-TripLedger': '1',
  };
}

function login() {
  if (!TEST_EMAIL || !TEST_PASSWORD) {
    throw new Error('TEST_EMAIL and TEST_PASSWORD are required');
  }
  if (!GROUP_ID) {
    throw new Error('GROUP_ID is required');
  }

  const response = http.post(
    `${BASE_URL}/api/auth/login`,
    JSON.stringify({ email: TEST_EMAIL, password: TEST_PASSWORD }),
    { headers: loginHeaders(), tags: { phase: 'login' } },
  );

  if (response.status !== 200) {
    throw new Error(`Login failed with HTTP ${response.status}`);
  }
  loggedIn = true;
}

function getDashboard(tags) {
  return http.get(`${BASE_URL}/api/groups/${GROUP_ID}/dashboard`, {
    headers: {
      Origin: __ENV.APP_ORIGIN || BASE_URL,
      'X-TripLedger': '1',
    },
    tags,
  });
}

function countCacheStatus(response) {
  const status = (response.headers['X-Cache'] || response.headers['x-cache'] || '').toUpperCase();
  if (status === 'HIT') cacheHit.add(1);
  else if (status === 'MISS') cacheMiss.add(1);
  else if (status === 'BYPASS') cacheBypass.add(1);
  else if (status === 'ERROR') cacheError.add(1);
}

export default function () {
  if (!loggedIn) {
    login();
  }

  if (!warmedUp) {
    const warmResponse = getDashboard({ phase: 'warmup' });
    check(warmResponse, {
      'warm-up returns 200': (r) => r.status === 200,
    });
    warmedUp = true;
  }

  const response = getDashboard({ phase: 'measured', endpoint: 'dashboard' });
  dashboardRequests.add(1);
  dashboardLatency.add(response.timings.duration);
  countCacheStatus(response);

  let body = null;
  try {
    body = response.json();
  } catch (_) {
    body = null;
  }
  const valid = check(response, {
    'dashboard returns 200': (r) => r.status === 200,
    'dashboard is for the requested group': () => body !== null && body.id === GROUP_ID,
  });
  dashboardErrors.add(!valid);
  sleep(1);
}

export function handleSummary(data) {
  const latency = data.metrics.dashboard_latency.values;
  const requests = data.metrics.dashboard_requests.values.count;
  const rps = data.metrics.dashboard_requests.values.rate;
  const errors = data.metrics.dashboard_errors.values.rate;
  const hit = data.metrics.cache_status_hit.values.count;
  const miss = data.metrics.cache_status_miss.values.count;
  const bypass = data.metrics.cache_status_bypass.values.count;
  const error = data.metrics.cache_status_error.values.count;

  const summary = [
    `p50: ${latency.med.toFixed(2)} ms`,
    `p95: ${latency['p(95)'].toFixed(2)} ms`,
    `request count: ${requests}`,
    `requests per second: ${rps.toFixed(2)}`,
    `error rate: ${(errors * 100).toFixed(2)}%`,
    `X-Cache HIT: ${hit}`,
    `X-Cache MISS: ${miss}`,
    `X-Cache BYPASS: ${bypass}`,
    `X-Cache ERROR: ${error}`,
  ].join('\n');

  return {
    stdout: `${summary}\n`,
  };
}
