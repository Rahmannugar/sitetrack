"""Capture plans from the SQL used by the application."""
import argparse
import datetime
import json
from pathlib import Path
import re
import shlex
import subprocess

parser = argparse.ArgumentParser()
parser.add_argument('--manifest', default='bin/seed-manifest.json')
parser.add_argument('--output', default='bin/query-plans')
parser.add_argument('--psql', default='psql', help='psql command, including connection flags')
args = parser.parse_args()
manifest = json.loads(Path(args.manifest).read_text())
if manifest['profile'] != 'full':
    parser.error('Use the full assessment seed')
project = manifest['projects'][0]
actor = manifest['accounts'][0]['id']
values = dict(actor=actor, project_id=project, search='', status='', direction='',
              sort_by='id', sort_order='asc', after_id='', after_value='', after_name='',
              after_quantity='0', after_status='out_of_stock', after_percentage=0,
              after_count=0, after_steps=0, late_only=False, page_size=21,
              today=datetime.datetime.now(datetime.timezone.utc).date().isoformat())

def literal(value):
    if value is None:
        return 'NULL'
    if isinstance(value, bool):
        return 'true' if value else 'false'
    if isinstance(value, int):
        return str(value)
    return "'" + value.replace("'", "''") + "'"

cases = [
    ('projects', 'projects', 'ListProjects', {}),
    ('projects', 'projects', 'ListProjectsBySummary', {'sort_by': 'completion_percentage'}),
    ('projects', 'projects', 'ListProjectsBySummary', {'sort_by': 'late_count', 'late_only': True}),
    ('tasks', 'tasks', 'ListTasks', {}),
    ('tasks', 'tasks', 'ListTasks', {'sort_by': 'dependent_count', 'blocked': True}),
    ('tasks', 'dependencies', 'ListDependents', { 'task_id': project+'-T-0001'}),
    ('inventory', 'inventory', 'ListCompanyStock', {'sort_by': 'name', 'after_id': None}),
    ('inventory', 'inventory', 'ListProjectStock', {'sort_by': 'quantity', 'after_id': None}),
    ('deliveries', 'deliveries', 'ListCompanyDeliveries', {'after_id': None}),
    ('deliveries', 'deliveries', 'ListProjectDeliveries', {'after_id': None}),
]
output = Path(args.output)
output.mkdir(parents=True, exist_ok=True)
summaries = []
for index, (domain, file, name, overrides) in enumerate(cases):
    source = Path(f'internal/{domain}/queries/{file}.sql').read_text()
    match = re.search(r'-- name: '+name+r' :\w+\n(.*?)(?=\n-- name:|\Z)', source, re.S)
    sql = match.group(1).strip().rstrip(';')
    settings = values | overrides
    def replace(match):
        kind, key = match.groups()
        return literal(settings.get(key) if kind == 'narg' else settings[key])
    sql = re.sub(r'sqlc\.(arg|narg)\((\w+)\)', replace, sql)
    if '$' in sql:
        raise ValueError('Unbound positional parameter')
    result = subprocess.run(shlex.split(args.psql)+['-X', '-qAt', '-v', 'ON_ERROR_STOP=1'],
                            input="SET statement_timeout='30s'; EXPLAIN (ANALYZE,BUFFERS,FORMAT JSON) "+sql+';',
                            text=True, capture_output=True, check=True)
    plan = json.loads(result.stdout)[0]
    path = output / f'{index+1:02d}-{name}.json'
    path.write_text(json.dumps(plan, indent=2)+'\n')
    summary = {'query': name, 'variant': overrides, 'execution_ms': plan['Execution Time'],
               'planning_ms': plan['Planning Time'], 'rows': plan['Plan']['Actual Rows'],
               'shared_hit_blocks': plan['Plan'].get('Shared Hit Blocks', 0),
               'shared_read_blocks': plan['Plan'].get('Shared Read Blocks', 0), 'file': str(path)}
    summaries.append(summary)
    print(json.dumps(summary), flush=True)
(output/'summary.json').write_text(json.dumps(summaries, indent=2)+'\n')
