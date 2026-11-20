// Agentrail's configuration, its two boundaries, and what actually leaves the process.
//
// The predicate tests are the inbound half: they are what stops an organization
// slug or a repository path in a REQUEST from reaching a vendor. The last test
// is behavioural on purpose, and it covers the half that a pure predicate
// cannot: an allowlisted request that calls out to GitHub. A path allowlist
// means nothing if the outbound calls made while serving an allowlisted request
// are exported with their own paths.

import { describe, it } from 'node:test'
import assert from 'node:assert/strict'
import { createServer as createHttpServer, request as httpRequest, type Server } from 'node:http'
import type { AddressInfo } from 'node:net'
import { randomUUID } from 'node:crypto'
import { z } from 'zod'
import { McpServer } from '@modelcontextprotocol/sdk/server/mcp.js'
import { WebStandardStreamableHTTPServerTransport } from '@modelcontextprotocol/sdk/server/webStandardStreamableHttp.js'
import { Client } from '@modelcontextprotocol/sdk/client/index.js'
import { StreamableHTTPClientTransport } from '@modelcontextprotocol/sdk/client/streamableHttp.js'
import {
  AGENTRAIL_ALLOWLIST,
  NO_AGENTRAIL,
  agentrailConfigFrom,
  agentrailPathAllowed,
  startAgentrail,
} from '../src/analytics/agentrail.ts'

function listening(server: Server): Promise<number> {
  return new Promise((resolve) => {
    server.listen(0, '127.0.0.1', () => resolve((server.address() as AddressInfo).port))
  })
}

function closed(server: Server): Promise<void> {
  return new Promise((resolve, reject) => {
    server.close((err) => (err ? reject(err) : resolve()))
  })
}

describe('Agentrail configuration', () => {
  it('is off when neither credential is set', () => {
    assert.equal(agentrailConfigFrom({}), null)
  })

  it('refuses a half configuration rather than starting and recording nothing', () => {
    assert.throws(
      () => agentrailConfigFrom({ AF_AGENTRAIL_SECRET: 'secret' }),
      /AF_AGENTRAIL_HASH_KEY is not set/,
    )
    assert.throws(
      () => agentrailConfigFrom({ AF_AGENTRAIL_HASH_KEY: 'hash' }),
      /AF_AGENTRAIL_SECRET is not set/,
    )
  })

  it('defaults the endpoint and keeps an explicit one', () => {
    const config = agentrailConfigFrom({
      AF_AGENTRAIL_SECRET: 'secret',
      AF_AGENTRAIL_HASH_KEY: 'hash',
    })
    assert.equal(config?.endpoint, 'https://api.agentrail.co/ingest/otlp')
    const custom = agentrailConfigFrom({
      AF_AGENTRAIL_SECRET: 'secret',
      AF_AGENTRAIL_HASH_KEY: 'hash',
      AF_AGENTRAIL_ENDPOINT: 'http://127.0.0.1:4318/ingest/otlp',
    })
    assert.equal(custom?.endpoint, 'http://127.0.0.1:4318/ingest/otlp')
  })
})

describe('the off facade', () => {
  it('passes the transport through unchanged and sends nothing', async () => {
    const facade = await startAgentrail(agentrailConfigFrom({}), { serviceName: 'test' })
    assert.equal(facade.enabled, false)
    const transport = { handleRequest: () => 'inner' }
    assert.equal(facade.mcpTransport(transport), transport)
    facade.identifyUser('user-1')
    await facade.shutdown()
  })

  it('never changes through the exported constant', () => {
    assert.equal(NO_AGENTRAIL.enabled, false)
    const transport = {}
    assert.equal(NO_AGENTRAIL.mcpTransport(transport), transport)
  })
})

describe('the path allowlist', () => {
  const allowed = [
    '/mcp',
    '/mcp?session=abc',
    '/auth/github',
    '/auth/github/callback?code=x&state=y',
    '/auth/email',
    '/auth/email/callback?token=t',
    '/auth/device/code',
    '/auth/device/approve',
    '/auth/mcp/authorize',
    '/auth/mcp/token',
    '/auth/mcp/register',
    '/.well-known/oauth-protected-resource',
    '/.well-known/oauth-protected-resource/mcp',
    '/.well-known/oauth-authorization-server',
  ]

  for (const path of allowed) {
    it(`accepts ${path}`, () => {
      assert.equal(agentrailPathAllowed(path), true)
    })
  }

  const refused = [
    '/v1/orgs/acme/environments',
    '/v1/orgs/acme/repos/acme/api/pulls',
    '/trpc/x',
    '/ph/e',
    '/mcpx',
    '/auth/githubx',
    '/auth/mcp',
    '/auth/device',
    '/console/settings',
    '/admin/customers',
    '/ph',
    '',
  ]

  for (const path of refused) {
    it(`refuses ${path || 'an empty url'}`, () => {
      assert.equal(agentrailPathAllowed(path), false)
    })
  }

  it('names every listed entry as either exact or a prefix', () => {
    for (const entry of AGENTRAIL_ALLOWLIST) {
      assert.ok(entry.startsWith('/'))
      if (entry.endsWith('*')) assert.ok(entry.length > 1)
    }
  })
})

describe('what leaves the process', () => {
  it('exports allowlisted requests only, and none of their outbound calls', async () => {
    const received: Buffer[] = []
    const collector = createHttpServer((request, response) => {
      request.on('data', (chunk: Buffer) => received.push(chunk))
      request.on('end', () => {
        response.writeHead(200, { 'content-type': 'application/x-protobuf' })
        response.end()
      })
    })
    const collectorPort = await listening(collector)

    // Stands in for GitHub. Every path it is asked for carries a customer name
    // that must not reach the collector, even though the request that asked for
    // it is allowlisted.
    const upstream = createHttpServer((request, response) => {
      response.writeHead(200, { 'content-type': 'application/json' })
      response.end(JSON.stringify({ path: request.url }))
    })
    const upstreamPort = await listening(upstream)

    const errors: string[] = []
    const facade = await startAgentrail(
      agentrailConfigFrom({
        AF_AGENTRAIL_SECRET: 'test-secret',
        AF_AGENTRAIL_HASH_KEY: 'test-hash-key-at-least-32-characters',
        AF_AGENTRAIL_ENDPOINT: `http://127.0.0.1:${collectorPort}/ingest/otlp`,
      }),
      {
        serviceName: 'antifailure-control-plane-test',
        onError: (error) => errors.push(error.code),
      },
    )
    assert.equal(facade.enabled, true)

    const mcpServer = new McpServer({ name: 'test', version: '1.0.0' })
    mcpServer.registerTool(
      'list_projects',
      { description: 'Read the repositories connected to the organization.', inputSchema: z.object({}) },
      async () => {
        const response = await fetch(`http://127.0.0.1:${upstreamPort}/repos/acme-secret-repo/actions`)
        await response.text()
        return { content: [{ type: 'text' as const, text: 'RESULT-SECRET-TOKEN' }] }
      },
    )
    const transport = new WebStandardStreamableHTTPServerTransport({
      enableJsonResponse: true,
      sessionIdGenerator: () => randomUUID(),
    })
    await mcpServer.connect(facade.mcpTransport(transport))

    let appPort = 0
    const app = createHttpServer(async (request, response) => {
      if (request.url === '/mcp' && request.method === 'POST') {
        const chunks: Buffer[] = []
        for await (const chunk of request) chunks.push(chunk as Buffer)
        const headers: Record<string, string> = {}
        for (const [name, value] of Object.entries(request.headers)) {
          if (typeof value === 'string') headers[name] = value
        }
        const forwarded = new Request(`http://127.0.0.1:${appPort}/mcp`, {
          method: 'POST',
          headers,
          body: Buffer.concat(chunks),
        })
        const answered = await transport.handleRequest(forwarded)
        answered.headers.forEach((value, name) => response.setHeader(name, value))
        response.writeHead(answered.status)
        response.end(Buffer.from(await answered.arrayBuffer()))
        return
      }

      if (request.url?.startsWith('/auth/github/callback')) {
        // Inside an ALLOWLISTED request, the shape of a real GitHub sign in.
        facade.identifyUser('user-1')
        const fetched = await fetch(
          `http://127.0.0.1:${upstreamPort}/orgs/acme-secret-org/memberships/octo`,
        )
        await fetched.text()
        await new Promise<void>((resolve, reject) => {
          const outgoing = httpRequest(
            {
              host: '127.0.0.1',
              port: upstreamPort,
              path: '/orgs/acme-node-org/members',
              method: 'GET',
            },
            (answer) => {
              answer.resume()
              answer.on('end', () => resolve())
            },
          )
          outgoing.on('error', reject)
          outgoing.end()
        })
        response.writeHead(200, { 'content-type': 'text/plain' })
        response.end('signed in')
        return
      }

      response.writeHead(404, { 'content-type': 'text/plain' })
      response.end('no')
    })
    appPort = await listening(app)

    try {
      const callback = await fetch(`http://127.0.0.1:${appPort}/auth/github/callback`)
      assert.equal(callback.status, 200)

      const client = new Client({ name: 'agentrail-test', version: '1.0.0' })
      await client.connect(
        new StreamableHTTPClientTransport(new URL(`http://127.0.0.1:${appPort}/mcp`)),
      )
      const result = await client.callTool({ name: 'list_projects', arguments: {} })
      const content = result.content as { type: string; text?: string }[]
      assert.equal(content[0]?.text, 'RESULT-SECRET-TOKEN')
      await client.close()
    } finally {
      await closed(app)
      await mcpServer.close()
      // Flushes the queue before it resolves.
      await facade.shutdown()
      await closed(upstream)
      await closed(collector)
    }

    // OTLP is protobuf, and its string fields are raw UTF-8 in the body, so a
    // substring check is a check on what the vendor would receive.
    const body = Buffer.concat(received).toString('utf8')
    assert.ok(received.length > 0, 'the collector received nothing at all')
    assert.ok(body.includes('/auth/github/callback'), 'the allowlisted auth path was not exported')
    assert.ok(body.includes('/mcp'), 'the allowlisted MCP path was not exported')
    assert.ok(body.includes('list_projects'), 'the MCP tool name was not exported')
    for (const customerName of [
      'acme-secret-org',
      'acme-node-org',
      'acme-secret-repo',
      'RESULT-SECRET-TOKEN',
    ]) {
      assert.ok(!body.includes(customerName), `${customerName} reached the collector`)
    }
    assert.deepEqual(
      errors.filter((code) => code === 'identity_outside_request'),
      [],
      'identify ran outside the request it authenticated',
    )
  })
})
