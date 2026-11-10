#!/usr/bin/env node
// A green application build is not enough: an out-of-date installer/Compose
// default silently deploys an older release. Keep every shipping entry aligned.
import assert from 'node:assert/strict'
import { readFileSync } from 'node:fs'
import { fileURLToPath } from 'node:url'
import path from 'node:path'

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..')
const read = (name) => readFileSync(path.join(root, name), 'utf8')
const version = JSON.parse(read('frontend/package.json')).version
assert.match(version, /^\d+\.\d+\.\d+(?:-(?:alpha|beta|rc)\.\d+)?$/, 'invalid product version')
const lock = JSON.parse(read('frontend/package-lock.json'))
assert.equal(lock.version, version, 'package-lock version drift')
assert.equal(lock.packages[''].version, version, 'package-lock root version drift')

for (const [file, expected] of [
  ['backend/pkg/config/config.go', `const AppVersion = "${version}"`],
  ['frontend/src/stores/system.ts', `const DEFAULT_SYSTEM_VERSION = "${version}"`],
  ['py-extractor/app/main.py', `version="${version}"`],
  ['scripts/install.sh', `DEFAULT_VERSION="v${version}"`],
  ['docker-compose.registry.yml', `\${EFFCHAT_VERSION:-v${version}}`],
  ['docs/deployment.md', `\${EFFCHAT_VERSION:-v${version}}`],
  ['CHANGELOG.md', `## [${version}] - `],
]) {
  assert.ok(read(file).includes(expected), `${file}: expected ${expected}`)
}
if (process.env.GITHUB_REF_NAME?.startsWith('v')) {
  assert.equal(process.env.GITHUB_REF_NAME, `v${version}`, 'release tag differs from product version')
}
console.log(`Release version aligned: v${version}`)
