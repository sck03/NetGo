import { test } from 'node:test'
import assert from 'node:assert/strict'
import { LatestQuery } from '../../packages/ui/query.ts'

const deferred = () => Promise.withResolvers()
test('a mutation refresh supersedes an earlier read, including its errors and cleanup', async () => {
  for (const staleFails of [false, true]) {
    const old = deferred(), fresh = deferred(), accepted = []
    let calls = 0
    const query = new LatestQuery(() => (++calls === 1 ? old.promise : fresh.promise), value => accepted.push(value))
    const first = query.run()
    assert.equal(query.run(), first)
    await Promise.resolve()
    query.invalidate()
    const second = query.run()
    await Promise.resolve()
    if (staleFails) old.reject(Error('stale failure')); else old.resolve('stale data')
    await first
    assert.equal(query.run(), second, 'old completion must not clear the new pending request')
    fresh.resolve('saved data')
    await second
    assert.deepEqual(accepted, ['saved data'])
    assert.equal(calls, 2)
  }
})
test('leaving the page suppresses a pending result; current failures remain visible and retryable', async () => {
  const pending = deferred(), accepted = []
  const query = new LatestQuery(() => pending.promise, value => accepted.push(value))
  const first = query.run()
  query.invalidate()
  pending.resolve('previous account')
  await first
  assert.deepEqual(accepted, [])
  let attempts = 0
  const retry = new LatestQuery(() => { if (++attempts === 1) throw Error('offline'); return Promise.resolve('recovered') }, value => accepted.push(value))
  await assert.rejects(retry.run(), /offline/)
  await retry.run()
  assert.deepEqual(accepted, ['recovered'])
})
