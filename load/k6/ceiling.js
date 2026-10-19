import http from 'k6/http';
import { check } from 'k6';

// Staged ceiling search: hold each configured rate for its duration in
// turn and report where 201s stop being the dominant response. This is a
// capacity probe, not a benchmark number - record the configuration and
// raw output in docs/benchmarks/ (no number without a run).
//
// Stages default: 25/s -> 50/s -> 100/s -> 200/s, 30s each. Override with
// CEILING_STAGES="50:30s,100:30s".
const baseURL = __ENV.BASE_URL || 'http://localhost:8080';
const targetID = __ENV.TARGET_ID;

if (!targetID) {
  throw new Error('TARGET_ID is required');
}

const stages = (__ENV.CEILING_STAGES || '25:30s,50:30s,100:30s,200:30s')
  .split(',')
  .map((s) => {
    const [rate, dur] = s.split(':');
    return { duration: dur, rate: Number(rate) };
  });

const stageSeconds = (d) => {
  const m = d.match(/^(\d+)([a-z]+)$/);
  if (!m) throw new Error(`bad duration: ${d}`);
  const mult = m[2] === 's' ? 1 : m[2] === 'm' ? 60 : m[2] === 'h' ? 3600 : 0;
  if (!mult) throw new Error(`bad duration unit: ${d}`);
  return Number(m[1]) * mult;
};

// One constant-arrival-rate scenario per stage, chained via startTime so
// each stage holds its own rate and the k6 summary reports per-scenario
// results: the ceiling answer is directly readable per stage.
const scenarios = {};
const thresholds = { checks: ['rate==1'], http_req_failed: ['rate==0'], dropped_iterations: ['count==0'] };
let start = 0;
stages.forEach((st, index) => {
  if (!Number.isInteger(st.rate) || st.rate <= 0) throw new Error('stage rate must be a positive integer');
  const name = `stage_${index + 1}_${st.rate}rps`;
  scenarios[name] = {
    executor: 'constant-arrival-rate',
    rate: st.rate,
    timeUnit: '1s',
    duration: st.duration,
    preAllocatedVUs: Number(__ENV.PRE_ALLOCATED_VUS || 20),
    maxVUs: Number(__ENV.MAX_VUS || 300),
    startTime: `${start}s`,
  };
  start += stageSeconds(st.duration);
  thresholds[`http_req_duration{scenario:${name}}`] = ['p(99)>=0'];
  thresholds[`checks{scenario:${name}}`] = ['rate==1'];
});

export const options = { scenarios, thresholds };

export default function () {
  const headers = {
    'Content-Type': 'application/json',
    'Idempotency-Key': `${__ENV.RUN_ID || 'ceiling'}-${__VU}-${__ITER}-${Date.now()}-${Math.random()}`,
  };
  if (__ENV.API_KEY) {
    headers['X-API-Key'] = __ENV.API_KEY;
  }

  const response = http.post(
    `${baseURL}/api/v1/targets/${targetID}/events`,
    JSON.stringify({ payload: { type: 'ceiling.test', vu: __VU, iteration: __ITER } }),
    { headers },
  );
  check(response, { 'event accepted': (r) => r.status === 201 });
}
