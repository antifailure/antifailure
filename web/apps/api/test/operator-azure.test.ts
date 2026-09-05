import { it } from 'node:test'
import assert from 'node:assert/strict'
import { EventEmitter } from 'node:events'
import { PassThrough } from 'node:stream'
const { deploymentTarget, remoteOperatorInit, connectOperator } = await import('../../../../deploy/cd/operator-init.mjs' as string)

const source = 'resource_group_name = "af-test"\nname = "afproof"\n'
const target = { group: 'af-test', app: 'afproof-app' }
const servingTarget = { ...target, revision: 'afproof-app-r1' }
function azure(overrides: Record<string, unknown> = {}) {
  const calls: unknown[][] = []
  const execute = (_: string, args: string[], options: unknown) => {
    calls.push([args, options])
    if (args[0] === 'group') return { status: 0, stdout: JSON.stringify({ name: target.group, project: 'antifailure', ...overrides }) }
    if (args[1] === 'show') return { status: 0, stdout: JSON.stringify({ name: target.app, resourceGroup: target.group, state: 'Running', traffic: [{ revisionName: servingTarget.revision, weight: 100 }] }) }
    return { status: 0 }
  }
  return { calls, execute }
}

it('resolves the exact deployment instead of selecting the first matching app', () => {
  assert.deepEqual(deploymentTarget('production', source), target)
})
it('requires an explicit known environment', () => {
  assert.throws(() => deploymentTarget('prod', source), /Choose production or staging/)
})
it('refuses foreign resource groups before Azure runs', () => {
  assert.throws(() => deploymentTarget('staging', source.replace('af-test', 'postiz-rg')), /outside Antifailure/)
})
it('refuses duplicate deployment identifiers', () => {
  assert.throws(() => deploymentTarget('staging', source + 'name = "second"\n'), /one literal name/)
})
it('requires the live project tag before opening the operator terminal', async () => {
  await assert.rejects(remoteOperatorInit(target, azure({ project: 'another-project' }).execute), /exact tagged Antifailure/)
})
it('opens the verified exact runtime after both Azure checks', async () => {
  const fake = azure()
  let connected
  await remoteOperatorInit(target, fake.execute, async (value: unknown) => { connected = value })
  assert.deepEqual(connected, servingTarget)
})
it('fails closed when Azure cannot verify the target', async () => {
  await assert.rejects(remoteOperatorInit(target, () => ({ status: 1, stderr: 'provider diagnostic' })), /could not verify/)
})
it('preserves the Azure refusal instead of hiding the reason behind a generic error', async () => {
  await assert.rejects(remoteOperatorInit(target, () => ({ status: 1, stderr: 'AuthorizationFailed: missing Reader permission' })), /AuthorizationFailed: missing Reader permission/)
})

function connection() {
  const child = Object.assign(new EventEmitter(), { stdout: new PassThrough() })
  let args: unknown
  const execute = (_: unknown, supplied: unknown) => { args = supplied; return child }
  return { child, execute, args: () => args }
}
const token = '00000000-0000-4000-8000-000000000001'
it('never sends a password or database URL in the Azure command arguments', async () => {
  const fake = connection()
  const pending = connectOperator(servingTarget, fake.execute, new PassThrough(), token)
  fake.child.stdout.write(`operator-init-complete:${token}\n`)
  fake.child.emit('close', 0)
  await pending
  assert.deepEqual(fake.args(), ['containerapp', 'exec', '--resource-group', 'af-test', '--name', 'afproof-app', '--revision', servingTarget.revision, '--command', `af-operator init --completion-token ${token}`])
})
it('does not confuse a clean Azure exit with a successful operator setup', async () => {
  const fake = connection()
  const pending = connectOperator(servingTarget, fake.execute, new PassThrough(), token)
  fake.child.stdout.write('The control plane already has a root operator.\n')
  fake.child.emit('close', 0)
  await assert.rejects(pending, /did not confirm completion/)
})

it('restores the local terminal even when remote setup is refused', async () => {
  const fake = connection()
  let restored = false
  const pending = connectOperator(servingTarget, fake.execute, new PassThrough(), token, () => { restored = true })
  fake.child.emit('close', 0)
  await pending.catch(() => {})
  assert.equal(restored, true)
})

for (const signal of ['SIGTERM', 'SIGHUP'] as const) {
  it(`terminates the Azure child when the wrapper receives ${signal}`, async () => {
    const fake = connection()
    let forwarded
    Object.assign(fake.child, { kill(value: string) { forwarded = value } })
    const pending = connectOperator(servingTarget, fake.execute, new PassThrough(), token)
    process.emit(signal)
    fake.child.emit('close', 1)
    await pending.catch(() => {})
    assert.equal(forwarded, signal)
  })
}

it('refuses split traffic instead of choosing an arbitrary running revision', async () => {
  const fake = azure()
  const execute = (command: string, args: string[], options: unknown) => {
    const result = fake.execute(command, args, options)
    if (args[0] === 'containerapp') {
      result.stdout = JSON.stringify({ name: target.app, resourceGroup: target.group, state: 'Running', traffic: [{ revisionName: 'first', weight: 50 }, { revisionName: 'second', weight: 50 }] })
    }
    return result
  }
  await assert.rejects(remoteOperatorInit(target, execute, async () => { throw new Error('connected') }), /one revision serving all traffic/)
})
