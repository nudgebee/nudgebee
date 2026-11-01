// Shared config and load profiles for the per-service k6 scripts.
// Every knob is an env var, so the same script runs locally (docker) and in-cluster (run.sh).
import { sleep } from 'k6';

export function env(name, fallback) {
  const v = __ENV[name];
  if (v !== undefined && v !== '') return v;
  if (fallback === undefined) throw new Error(`${name} is required`);
  return fallback;
}

export function baseUrl(name, fallback) {
  return env(name, fallback).replace(/\/+$/, '');
}

const whole = (n) => Math.max(1, Math.round(n));

// Open-model (arrival-rate) profiles: k6 starts iterations on schedule even when the
// service slows down, so saturation shows up as latency and dropped_iterations instead
// of being hidden behind VUs that are stuck waiting.
//   RATE       peak iterations per TIME_UNIT (default 10)
//   TIME_UNIT  default 1s; scripts with long iterations pass a longer default
//   DURATION   length of smoke / baseline / soak
//   MAX_VUS    cap on concurrent in-flight iterations (default 200)
// gracefulStop is how long in-flight iterations may run on after the last stage.
export function scenarios(exec, { timeUnit: defaultTimeUnit = '1s', gracefulStop = '30s' } = {}) {
  const profile = env('PROFILE', 'smoke');
  const rate = Number(env('RATE', '10'));
  // Stages are whole iterations per TIME_UNIT, so below 4 the ramp rounds to a flat line.
  if ((profile === 'stress' || profile === 'spike') && rate < 4) {
    throw new Error(`${profile} needs RATE >= 4 so its steps differ; for a lower rate use a longer TIME_UNIT (RATE=60 TIME_UNIT=1m is 1/s)`);
  }
  const timeUnit = env('TIME_UNIT', defaultTimeUnit);
  const maxVUs = Number(env('MAX_VUS', '200'));
  const common = { exec, timeUnit, gracefulStop };
  const vus = { preAllocatedVUs: Math.min(maxVUs, whole(rate * 2)), maxVUs };
  const constant = (r, duration) => ({ executor: 'constant-arrival-rate', ...common, rate: whole(r), duration, ...vus });
  const ramping = (startRate, stages) => ({
    executor: 'ramping-arrival-rate',
    ...common,
    startRate: whole(startRate),
    stages: stages.map(([target, duration]) => ({ target: whole(target), duration })),
    ...vus,
  });

  switch (profile) {
    case 'smoke':
      return { smoke: { executor: 'constant-arrival-rate', ...common, rate: 1, duration: env('DURATION', '30s'), preAllocatedVUs: 2, maxVUs: 10 } };
    case 'baseline':
      return { baseline: constant(rate, env('DURATION', '5m')) };
    case 'stress':
      return {
        stress: ramping(1, [
          [rate * 0.25, '2m'],
          [rate * 0.5, '3m'],
          [rate * 0.75, '3m'],
          [rate, '3m'],
          [rate, '2m'],
          [1, '1m'],
        ]),
      };
    case 'soak':
      return { soak: constant(rate * 0.7, env('DURATION', '1h')) };
    case 'users':
      // Closed model: VUS users each loop request, think time, request for DURATION.
      return { users: { executor: 'constant-vus', exec, vus: Number(env('VUS', '20')), duration: env('DURATION', '5m'), gracefulStop } };
    case 'spike':
      return {
        spike: ramping(rate * 0.1, [
          [rate * 0.1, '1m'],
          [rate, '10s'],
          [rate, '1m'],
          [rate * 0.1, '10s'],
          [rate * 0.1, '2m'],
        ]),
      };
    default:
      throw new Error(`unknown PROFILE ${profile}; use smoke, baseline, stress, soak, spike or users`);
  }
}

// The abort line is a safety valve for shared environments: once more than 20% of
// requests fail for 30s the service is past its knee and further load only hurts
// other users of the environment.
// Pass request names to also get a per-name p95 line in the end-of-test summary.
export function thresholds(p95Ms, names = []) {
  const p95 = `p(95)<${env('P95_MS', String(p95Ms))}`;
  const t = {
    http_req_failed: [{ threshold: 'rate<0.01' }, { threshold: 'rate<0.2', abortOnFail: true, delayAbortEval: '30s' }],
    http_req_duration: [p95],
  };
  for (const n of names) t[`http_req_duration{name:${n}}`] = [p95];
  return t;
}

// Picks an entry from [{weight, ...}] proportionally to weight.
export function weightedPicker(items) {
  const total = items.reduce((sum, it) => sum + (it.weight || 1), 0);
  return () => {
    let r = Math.random() * total;
    for (const it of items) {
      r -= it.weight || 1;
      if (r <= 0) return it;
    }
    return items[items.length - 1];
  };
}

// Pause between one user's requests under the users profile, THINK_TIME seconds ("1-3"
// picks uniformly in that range). Arrival-rate profiles skip it: there a pause only holds VUs.
export function thinkTime() {
  if (env('PROFILE', 'smoke') !== 'users') return;
  const [lo, hi = lo] = env('THINK_TIME', '1-3').split('-').map(Number);
  sleep(lo + Math.random() * (hi - lo));
}
