import { it } from 'node:test'
import assert from 'node:assert/strict'
import { PassThrough } from 'node:stream'
import type { ReadStream, WriteStream } from 'node:tty'
import { initializeOperator } from '../src/admin/operator-command.ts'
import { operatorInput } from '../src/admin/operator-input.ts'
import type { BootstrapOperatorInput, BootstrapOperatorResult } from '../src/admin/bootstrap.ts'

const result: BootstrapOperatorResult = { adminUserId: 'test', email: 'operator@example.test', name: 'Operator', role: 'owner', applied: true, auditSeq: 1 }
const base = { adminUrl: 'postgres://runtime-only', email: result.email, name: result.name, password: 'test-only-operator-password' }

it('uses the runtime credential and the existing bootstrap guard', async () => {
  let received: BootstrapOperatorInput | undefined
  await initializeOperator({ ...base, bootstrap: async (input) => { received = input; return result } })
  assert.deepEqual(received, { ...base, operator: 'operator init' })
})

it('refuses a runtime with no privileged credential before asking for a password', async () => {
  await assert.rejects(initializeOperator({ prompt: async () => { throw new Error('prompted') } }), /no AF_ADMIN_DATABASE_URL/)
})

it('keeps the first-operator refusal instead of resetting an existing account', async () => {
  await assert.rejects(initializeOperator({ ...base, bootstrap: async () => { throw new Error('already has a root operator') } }), /already has a root operator/)
})

it('confirms an interactive password before any write', async () => {
  await assert.rejects(initializeOperator({ ...base, password: undefined,
    prompt: async (label) => label.startsWith('Confirm') ? 'different' : 'test-only-operator-password',
    bootstrap: async () => { throw new Error('wrote') },
  }), /passwords did not match/)
})

it('prompts for both identity fields and hides both password entries', async () => {
  const calls: boolean[] = []
  const answers = [result.email, result.name, base.password, base.password]
  await initializeOperator({ adminUrl: base.adminUrl, prompt: async (_, secret) => { calls.push(secret); return answers.shift()! }, bootstrap: async () => result })
  assert.deepEqual(calls, [false, false, true, true])
})

it('points the completed setup at the real operator route', async () => {
  assert.equal(await initializeOperator({ ...base, publicUrl: 'https://control.example.test/console', bootstrap: async () => result }), 'The first operator is ready. Sign in at https://control.example.test/admin')
})

it('rejects a malformed public URL before writing the first operator', async () => {
  await assert.rejects(initializeOperator({ ...base, publicUrl: 'not-a-url', bootstrap: async () => { throw new Error('wrote') } }), /AF_PUBLIC_URL must/)
})

it('rejects credentials embedded in the public URL without printing them', async () => {
  await assert.rejects(initializeOperator({ ...base, publicUrl: 'https://name:sensitive@example.test', bootstrap: async () => { throw new Error('wrote') } }), { message: 'AF_PUBLIC_URL must be an HTTP or HTTPS address without credentials. Nothing was written.' })
})

function terminal() {
  const input = new PassThrough() as unknown as ReadStream
  const output = new PassThrough() as unknown as WriteStream
  let written = ''
  Object.assign(input, { isTTY: true, isRaw: false, setRawMode(value: boolean) { input.isRaw = value; return input } })
  Object.assign(output, { isTTY: true })
  output.on('data', (value) => { written += String(value) })
  return { input, output, written: () => written }
}

it('does not echo a pasted password into terminal output', async () => {
  const t = terminal()
  const answer = operatorInput('Password: ', true, t.input, t.output)
  for (const value of 'test-only-secret') t.input.emit('keypress', value, {})
  t.input.emit('keypress', '\r', { name: 'return' })
  await answer
  assert.equal(t.written(), 'Password: \n')
})

it('returns the actual secret rather than masking the value passed to bootstrap', async () => {
  const t = terminal()
  const answer = operatorInput('Password: ', true, t.input, t.output)
  t.input.emit('keypress', 'passphrase', {})
  t.input.emit('keypress', '\r', { name: 'return' })
  assert.equal(await answer, 'passphrase')
})

it('restores terminal echo after successful password entry', async () => {
  const t = terminal()
  const answer = operatorInput('Password: ', true, t.input, t.output)
  t.input.emit('keypress', '\r', { name: 'return' })
  await answer
  assert.equal(t.input.isRaw, false)
})

it('cancels on interrupt instead of submitting a partial password', async () => {
  const t = terminal()
  const answer = operatorInput('Password: ', true, t.input, t.output)
  t.input.emit('keypress', '\u0003', { name: 'c', ctrl: true })
  await assert.rejects(answer, /Setup cancelled/)
})

it('refuses nonterminal interactive input', async () => {
  const t = terminal()
  Object.assign(t.input, { isTTY: false })
  await assert.rejects(operatorInput('Password: ', true, t.input, t.output), /needs a terminal/)
})

it('handles backspace without placing a secret character on screen', async () => {
  const t = terminal()
  const answer = operatorInput('Password: ', true, t.input, t.output)
  t.input.emit('keypress', 'ab', {})
  t.input.emit('keypress', '\u007f', { name: 'backspace' })
  t.input.emit('keypress', '\r', { name: 'return' })
  assert.equal(await answer, 'a')
})
