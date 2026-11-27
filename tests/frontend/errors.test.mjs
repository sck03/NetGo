import { test } from 'node:test'
import assert from 'node:assert/strict'
import { requestError, RequestError } from '../../packages/ui/errors.ts'

test('administrative invariants and uncertain writes have actionable messages', () => {
  assert.match(requestError('last_permanent_administrator'), /不过期/)
  assert.match(requestError('account_creation_uncertain: unexpected EOF'), /请勿重复创建/)
  assert.match(requestError('remote_user_inactive'), /无法授予/)
  assert.match(requestError('edit_conflict'), /重新打开编辑/)
  assert.match(requestError('control_unavailable'), /稍后重试/)
  const conflict = new RequestError('edit_conflict', 409)
  assert.equal(conflict.code, 'edit_conflict')
  assert.equal(conflict.status, 409)
  assert.equal(conflict.message, requestError('edit_conflict'))
  assert.equal(requestError(undefined), '请求失败')
  assert.equal(requestError('new_server_error'), 'new_server_error')
})
