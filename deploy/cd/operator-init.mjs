#!/usr/bin/env node
import { readFileSync } from 'node:fs'
import { resolve, dirname } from 'node:path'
import { fileURLToPath } from 'node:url'
import { spawn, spawnSync } from 'node:child_process'
import { randomUUID } from 'node:crypto'

/** Read only the two exact literal identifiers the deployment already uses. */
export function deploymentTarget(environment, source) {
  if (!['production', 'staging'].includes(environment)) throw new Error('Choose production or staging explicitly.')
  const literal = (name) => {
    const matches = [...source.matchAll(new RegExp(`^${name}\\s*=\\s*"([a-z0-9-]+)"\\s*$`, 'gm'))]
    if (matches.length !== 1) throw new Error(`The deployment must declare one literal ${name}.`)
    return matches[0][1]
  }
  const group = literal('resource_group_name')
  const name = literal('name')
  if (!group.startsWith('af-')) throw new Error('Refusing a resource group outside Antifailure.')
  return { group, app: `${name}-app` }
}

export async function remoteOperatorInit(target, execute = spawnSync, connect = connectOperator) {
  const inspect = (args) => {
    const result = execute('az', args, { encoding: 'utf8', stdio: ['ignore', 'pipe', 'pipe'] })
    if (result.status !== 0) {
      const detail = result.stderr?.trim() || result.error?.message || `exit status ${result.status}`
      throw new Error(`Azure could not verify the deployment: ${detail}`)
    }
    try { return JSON.parse(result.stdout) } catch { throw new Error('Azure returned an unreadable deployment response.') }
  }
  const group = inspect(['group', 'show', '--name', target.group, '--query', '{name:name,project:tags.project}', '--output', 'json'])
  if (group.name !== target.group || group.project !== 'antifailure') {
    throw new Error('The resource group is not the exact tagged Antifailure deployment. Nothing was changed.')
  }
  const app = inspect(['containerapp', 'show', '--resource-group', target.group, '--name', target.app,
    '--query', '{name:name,resourceGroup:resourceGroup,state:properties.runningStatus,traffic:properties.configuration.ingress.traffic,latest:properties.latestRevisionName}', '--output', 'json'])
  if (app.name !== target.app || app.resourceGroup !== target.group || app.state !== 'Running') {
    throw new Error('The exact application is not running. Nothing was changed.')
  }
  const serving = Array.isArray(app.traffic) ? app.traffic.filter((entry) => entry && entry.weight > 0) : []
  if (serving.length !== 1 || serving[0].weight !== 100) {
    throw new Error('The application does not have one revision serving all traffic. Resolve the deployment split before operator setup.')
  }
  const revision = serving[0].latestRevision ? app.latest : serving[0].revisionName
  if (typeof revision !== 'string' || !/^[a-z0-9-]+$/.test(revision)) throw new Error('Azure did not identify the serving revision.')
  await connect({ ...target, revision })
}

/** A successful Azure websocket exit is not proof the remote command succeeded. */
export async function connectOperator(target, execute = spawn, output = process.stdout, token = randomUUID(), restoreTerminal = saveTerminal()) {
  const marker = `operator-init-complete:${token}`
  const child = execute('az', ['containerapp', 'exec', '--resource-group', target.group, '--name', target.app, '--revision', target.revision,
    '--command', `af-operator init --completion-token ${token}`], { stdio: ['inherit', 'pipe', 'inherit'] })
  let cancelled = false
  let forceStop
  const signals = ['SIGINT', 'SIGTERM', 'SIGHUP']
  const forwarders = new Map(signals.map((signal) => [signal, () => {
    cancelled = true
    child.kill(signal)
    forceStop ??= setTimeout(() => child.kill('SIGKILL'), 2000)
    forceStop.unref()
  }]))
  for (const [signal, forward] of forwarders) process.on(signal, forward)
  try { await new Promise((resolve, reject) => {
    let tail = ''
    let completed = false
    child.stdout.on('data', (chunk) => {
      output.write(chunk)
      tail = (tail + chunk.toString()).slice(-4096)
      completed ||= tail.includes(marker)
    })
    child.on('error', () => reject(new Error('Azure could not open the setup terminal. Check that the Azure CLI is installed and signed in.')))
    child.on('close', (status) => {
      if (status !== 0 || !completed || cancelled) {
        reject(new Error('The remote setup did not confirm completion. No password reset was attempted. Read the setup refusal above.'))
      } else resolve()
    })
  }) } finally {
    for (const [signal, forward] of forwarders) process.removeListener(signal, forward)
    clearTimeout(forceStop)
    restoreTerminal()
  }
}

function saveTerminal() {
  if (!process.stdin.isTTY || process.platform === 'win32') return () => {}
  const state = spawnSync('stty', ['-g'], { encoding: 'utf8', stdio: ['inherit', 'pipe', 'ignore'] })
  if (state.status !== 0) throw new Error('Could not save terminal settings. Setup has not started.')
  return () => {
    const restored = spawnSync('stty', [state.stdout.trim()], { stdio: ['inherit', 'ignore', 'inherit'] })
    if (restored.status !== 0) throw new Error('Could not restore the terminal. Run stty sane before continuing.')
  }
}

if (process.argv[1] && resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  try {
    const environment = process.argv[2]
    if (process.argv.length !== 3 || !['production', 'staging'].includes(environment)) {
      throw new Error('Run just operator-init production or just operator-init staging.')
    }
    if (!process.stdin.isTTY) throw new Error('Open an interactive terminal to run operator setup. Password entry happens inside the runtime.')
    const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..')
    const target = deploymentTarget(environment, readFileSync(resolve(root, `infra/terraform/stacks/control-plane/${environment}.tfvars`), 'utf8'))
    console.log(`Creating the first operator on ${environment}: ${target.group}/${target.app}.`)
    console.log('An existing root operator is never replaced. Password entry happens in the remote runtime.')
    await remoteOperatorInit(target)
  } catch (error) {
    console.error(error instanceof Error ? error.message : 'Operator setup failed.')
    process.exitCode = 1
  }
}
