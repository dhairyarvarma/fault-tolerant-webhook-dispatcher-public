import http from 'k6/http';
import { check } from 'k6';

// Exercise the public ingestion boundary. Create a target first, then pass
// its ID with TARGET_ID. Retain results in docs/benchmarks/ only after a
// reproducible run with its configuration and environment recorded.
const baseURL = __ENV.BASE_URL || 'http://localhost:8080';
const targetID = __ENV.TARGET_ID;

if (!targetID) {
  throw new Error('TARGET_ID is required');
}

export const options = {
  thresholds: { checks: ['rate==1'], http_req_failed: ['rate==0'], dropped_iterations: ['count==0'] },
  scenarios: {
    ingestion: {
      executor: 'constant-arrival-rate',
      rate: Number(__ENV.RATE || 25),
      timeUnit: '1s',
      duration: __ENV.DURATION || '30s',
      preAllocatedVUs: Number(__ENV.PRE_ALLOCATED_VUS || 10),
      maxVUs: Number(__ENV.MAX_VUS || 100),
    },
  },
};

export default function () {
  const headers = {
    'Content-Type': 'application/json',
    'Idempotency-Key': `${__ENV.RUN_ID || 'ingestion'}-${__VU}-${__ITER}-${Date.now()}`,
  };
  if (__ENV.API_KEY) {
    headers['X-API-Key'] = __ENV.API_KEY;
  }

  const response = http.post(
    `${baseURL}/api/v1/targets/${targetID}/events`,
    JSON.stringify({ payload: { type: 'load.test', vu: __VU, iteration: __ITER } }),
    { headers },
  );
  check(response, { 'event accepted': (r) => r.status === 201 });
}
