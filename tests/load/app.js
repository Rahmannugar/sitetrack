import { check, fail } from 'k6';
import exec from 'k6/execution';
import { Trend } from 'k6/metrics';
const endpointDuration = new Trend('read_endpoint_duration', true);
import http from 'k6/http';

const manifest = JSON.parse(open(__ENV.SEED_MANIFEST || '../../bin/seed-manifest.json'));
const base = (__ENV.BASE_URL || 'http://127.0.0.1:8080').replace(/\/$/, '');
const writes = __ENV.MODE === 'deliveries';
const taskNumber = manifest.profile === 'full' ? '2400' : '0040';
export const options = {
  scenarios: {
    app: { executor: 'constant-arrival-rate', rate: Number(__ENV.RATE || 20), timeUnit: '1s',
      duration: __ENV.DURATION || '60s', preAllocatedVUs: Number(__ENV.VUS || 20), maxVUs: Number(__ENV.VUS || 20) },
  },
  summaryTrendStats: ['avg', 'min', 'med', 'max', 'p(90)', 'p(95)', 'p(99)'],
  thresholds: {
    ...(!writes ? Object.fromEntries(readCases({ project: 'sample' }).map(([name]) => [`read_endpoint_duration{endpoint:${name}}`, ['p(99)<1000']])) : {}),
    http_req_failed: ['rate<0.01'], checks: ['rate==1'], dropped_iterations: ['count==0'],
    ...(!writes ? { 'http_req_duration{phase:read}': ['p(95)<500', 'p(99)<1000'] } : {}),
    ...(writes ? { 'http_req_duration{phase:write}': ['p(95)<1000'] } : {}),
  },
};
function params(token, name, phase = 'read') {
  return { headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
    tags: { name, phase }, timeout: '10s' };
}
function login(email) {
  const response = http.post(`${base}/auth/login`, JSON.stringify({ email, password: __ENV.SITETRACK_SEED_PASSWORD || 'SiteTrack123!' }),
    { headers: { 'Content-Type': 'application/json' }, tags: { name: 'login', phase: 'setup' } });
  if (response.status !== 200 || !response.json('access_token')) fail(`Seed login failed (${response.status})`);
  return response.json('access_token');
}
export function setup() {
  if (manifest.profile !== 'full' && __ENV.ALLOW_SMALL !== 'true') fail('Use the full assessment seed');
  const data = { tokens: {}, project: manifest.projects[0], material: manifest.materials[0] };
  for (const role of ['superadmin', 'manager', 'admin', 'member', 'outsider']) {
    data.tokens[role] = login(`${role}@example.test`);
  }
  const response = http.get(`${base}/projects/${data.project}`, params(data.tokens.admin, 'seed_project', 'setup'));
  if (response.status !== 200 || response.json('id') !== data.project) fail('API does not match the seed manifest');
  const hidden = http.get(`${base}/projects/${data.project}`, {
    ...params(data.tokens.outsider, 'outsider_project', 'setup'), responseCallback: http.expectedStatuses(404),
  });
  if (hidden.status !== 404) fail('Project isolation failed');
  if (writes) {
    data.company = http.get(`${base}/company-store/${data.material}`, params(data.tokens.manager, 'stock_before', 'setup')).json();
    data.projectStock = http.get(`${base}/projects/${data.project}/store/${data.material}`, params(data.tokens.admin, 'project_stock_before', 'setup')).json();
  }
  return data;
}
function readCases(data) {
  const p = `/projects/${data.project}`;
  return [
    ['me', '/auth/me', 'member'],
    ['projects_member', '/projects?limit=20', 'member'],
    ['projects_all', '/projects?limit=20', 'superadmin'],
    ['projects_completion', '/projects?sort=completion_percentage&limit=20', 'superadmin'],
    ['projects_late', '/projects?late_only=true&sort=late_count&limit=20', 'superadmin'],
    ['projects_search', '/projects?search=Bridge&sort=name&limit=20', 'superadmin'],
    ['project', p, 'member'],
    ['tasks', `${p}/tasks?limit=20`, 'member'],
    ['tasks_blocked', `${p}/tasks?blocked=true&sort=dependent_count&limit=20`, 'member'],
    ['tasks_late', `${p}/tasks?late=true&sort=end_date&limit=20`, 'member'],
    ['task', `${p}/tasks/${data.project}-T-${taskNumber}`, 'member'],
    ['relationships', `${p}/tasks/${data.project}-T-${taskNumber}/relationships`, 'member'],
    ['dependents', `${p}/tasks/${data.project}-T-0001/relationships?direction=dependents`, 'member'],
    ['members', `${p}/members`, 'member'],
    ['project_store', `${p}/store?sort=quantity&limit=20`, 'member'],
    ['company_store', '/company-store?limit=20', 'manager'],
    ['company_stock_status', '/company-store?status=low_stock&sort=quantity&limit=20', 'manager'],
    ['company_deliveries', '/company-store/deliveries?limit=20', 'manager'],
    ['company_deliveries_filtered', `/company-store/deliveries?project_id=${data.project}&status=delivered&limit=20`, 'manager'],
    ['project_deliveries', `${p}/deliveries?limit=20`, 'member'],
    ['materials', '/materials?search=construction&limit=20', 'member'],
    ['categories', '/material-categories?limit=20', 'member'],
    ['notifications', '/notifications?limit=20', 'member'],
    ['unread', '/notifications/unread-count', 'member'],
  ];
}
export default function (data) {
  if (writes) return delivery(data);
  const cases = readCases(data);
  const [name, path, role] = cases[exec.scenario.iterationInTest % cases.length];
  const response = http.get(base + path, params(data.tokens[role], name));
  endpointDuration.add(response.timings.duration, { endpoint: name });
  check(response, { 'read succeeds': r => r.status === 200 });
  if (response.status !== 200) return;
  const body = response.json();
  if (Array.isArray(body.items)) {
    check(body, { 'page bounded': b => b.items.length <= 20 });
    if (body.next_cursor) {
      const next = http.get(base + path + (path.includes('?') ? '&' : '?') + `cursor=${encodeURIComponent(body.next_cursor)}`,
        params(data.tokens[role], `${name}_page2`));
      endpointDuration.add(next.timings.duration, { endpoint: name });
      check(next, { 'cursor page succeeds': r => r.status === 200,
        'cursor advances': r => r.status === 200 && (!r.json('items').length || JSON.stringify(r.json('items')[0]) !== JSON.stringify(body.items[0])) });
    }
  }
}
function delivery(data) {
  const send = (direction, role) => http.post(`${base}/deliveries`, JSON.stringify({ project_id: data.project,
    direction, items: [{ material_id: data.material, quantity: '0.001' }] }),
    params(data.tokens[role], `send_${direction}`, 'write'));
  const response = send('company_to_project', 'manager');
  if (!check(response, { 'delivery sent': r => r.status === 201 })) return;
  const id = response.json('id');
  if (exec.scenario.iterationInTest % 2 === 0) {
    const received = http.post(`${base}/deliveries/${id}/receive`, null, params(data.tokens.admin, 'receive_project', 'write'));
    if (!check(received, { 'project receives stock': r => r.status === 200 })) return;
    const back = send('project_to_company', 'admin');
    if (!check(back, { 'return delivery sent': r => r.status === 201 })) return;
    const returned = http.post(`${base}/deliveries/${back.json('id')}/receive`, null, params(data.tokens.manager, 'receive_company', 'write'));
    check(returned, { 'company receives returned stock': r => r.status === 200 });
  } else {
    const requests = [0, 1].map(() => ({ method: 'POST', url: `${base}/deliveries/${id}/cancel`,
      params: { ...params(data.tokens.manager, 'race_cancel', 'write'), responseCallback: http.expectedStatuses(200, 409) } }));
    const results = http.batch(requests);
    check(results, { 'exactly one cancellation succeeds': rows => rows.filter(r => r.status === 200).length === 1 && rows.filter(r => r.status === 409).length === 1 });
  }
}
export function teardown(data) {
  if (writes) {
    const company = http.get(`${base}/company-store/${data.material}`, params(data.tokens.manager, 'stock_after', 'teardown')).json();
    const project = http.get(`${base}/projects/${data.project}/store/${data.material}`, params(data.tokens.admin, 'project_stock_after', 'teardown')).json();
    check(company, { 'company stock restored': value => value.quantity === data.company.quantity && value.in_transit_to_projects === data.company.in_transit_to_projects });
    check(project, { 'project stock restored': value => value.quantity === data.projectStock.quantity });
  }
  for (const token of Object.values(data.tokens)) {
    http.post(`${base}/auth/logout`, null, params(token, 'logout', 'teardown'));
  }
}

export function handleSummary(data) {
  const clean = { metrics: data.metrics, root_group: data.root_group };
  return { [__ENV.SUMMARY_PATH || 'bin/k6-summary.json']: JSON.stringify(clean, null, 2),
    stdout: JSON.stringify(clean.metrics, null, 2) + '\n' };
}
