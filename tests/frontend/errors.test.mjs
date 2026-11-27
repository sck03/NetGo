import { test } from 'node:test'
import assert from 'node:assert/strict'
import { requestError } from '../../packages/ui/errors.ts'

test('administrative invariants and uncertain writes have actionable messages', () => {
  assert.match(requestError('last_permanent_administrator'), /不过期/)
  assert.match(requestError('account_creation_uncertain: unexpected EOF'), /请勿重复创建/)
  assert.match(requestError('remote_user_inactive'), /无法授予/)
  assert.equal(requestError(undefined), '请求失败')
  assert.equal(requestError('new_server_error'), 'new_server_error')
})
