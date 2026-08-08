const assert = require('node:assert/strict');
const fs = require('node:fs');
const path = require('node:path');
const test = require('node:test');

const repositoryRoot = path.resolve(__dirname, '..', '..', '..');
const workflowPath = path.join(repositoryRoot, '.github', 'workflows', 'test.yml');

function positionOf(workflow, expression, description) {
  const position = workflow.search(expression);
  assert.notEqual(position, -1, `workflow must ${description}`);
  return position;
}

test('CI installs and checks the exact signed baseline before invoking it', () => {
  const workflow = fs.readFileSync(workflowPath, 'utf8');

  const contract = positionOf(
    workflow,
    /\.\s+\.\/tools\/versions\.env|source\s+tools\/versions\.env/,
    'load tools/versions.env',
  );
  const go = positionOf(workflow, /actions\/setup-go@v\d+/, 'install Go');
  const rust = positionOf(workflow, /rustup\s+toolchain\s+install/, 'install Rust');
  const node = positionOf(workflow, /actions\/setup-node@v\d+/, 'install Node');
  const supabase = positionOf(
    workflow,
    /supabase_\$\{SUPABASE_CLI_VERSION\}_linux_amd64\.tar\.gz/,
    'download the exact Supabase CLI release',
  );
  const compose = positionOf(
    workflow,
    /docker-compose-linux-x86_64/,
    'download the exact Docker Compose plugin release',
  );
  const checksums = positionOf(workflow, /sha256sum\s+--check\s+--status/, 'verify release checksums');
  const supabaseGate = positionOf(workflow, /supabase\s+--version/, 'gate the Supabase CLI version');
  const composeGate = positionOf(workflow, /docker compose version --short/, 'gate the Compose version');
  const baseline = positionOf(workflow, /scripts\/verify-baseline\.sh --ci/, 'invoke the shared CI baseline');

  assert.ok(contract < go && contract < rust && contract < node, 'tool contract must precede compiler setup');
  assert.ok(supabase < checksums && compose < checksums, 'both release downloads must precede checksum validation');
  assert.ok(checksums < supabaseGate && checksums < composeGate, 'checksums must be verified before version gates');
  assert.ok(supabaseGate < baseline && composeGate < baseline, 'CLI gates must run before the shared baseline');
  assert.match(workflow, /cargo test --locked|scripts\/verify-baseline\.sh --ci/, 'Rust must use locked resolution');
  assert.match(workflow, /actions\/upload-artifact@v\d+/, 'Playwright artifacts must remain uploaded');
});
