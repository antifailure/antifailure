// Sending one message, over a provider the environment can contain.
//
// The provider is Resend's HTTP API rather than SMTP, and that is the whole
// reason this file exists in the shape it does. A preview environment answers
// api.resend.com from the egress sidecar in capture mode: the request is read,
// the message is recorded into an inbox an agent can query, the provider's own
// success shape comes back, and nothing is delivered to anybody. SMTP would
// need a server on a port the sidecar does not terminate, so the message would
// either be delivered for real or fail, and both of those are wrong in a
// preview.
//
// So this is an interface with two implementations and no third: one that
// speaks to the API, and one that keeps what it was given, for tests.

export interface Message {
  readonly to: string
  readonly subject: string
  readonly text: string
  readonly html: string
}

export interface Mailer {
  send(message: Message): Promise<void>
}

export class MailError extends Error {}

/** Sends through Resend. */
export class ResendMailer implements Mailer {
  readonly #apiKey: string
  readonly #from: string
  readonly #baseUrl: string
  readonly #timeoutMs: number

  constructor(options: {
    apiKey: string
    /** The From address. Resend refuses a domain it has not verified, which is
     *  a configuration error worth failing loudly on rather than retrying. */
    from: string
    baseUrl?: string
    timeoutMs?: number
  }) {
    this.#apiKey = options.apiKey
    this.#from = options.from
    this.#baseUrl = options.baseUrl ?? 'https://api.resend.com'
    this.#timeoutMs = options.timeoutMs ?? 10_000
  }

  async send(message: Message): Promise<void> {
    // A timeout rather than the platform default. The caller does not await
    // this on the request path, but an outbound request with no deadline can
    // hold a socket for as long as the far end feels like, and a handful of
    // those is a process that stops accepting connections.
    const signal = AbortSignal.timeout(this.#timeoutMs)
    let res: Response
    try {
      res = await fetch(`${this.#baseUrl}/emails`, {
        method: 'POST',
        headers: {
          authorization: `Bearer ${this.#apiKey}`,
          'content-type': 'application/json',
        },
        body: JSON.stringify({
          from: this.#from,
          to: message.to,
          subject: message.subject,
          text: message.text,
          html: message.html,
        }),
        signal,
      })
    } catch (err) {
      throw new MailError(
        `The mail provider could not be reached: ${err instanceof Error ? err.message : String(err)}`,
      )
    }
    if (!res.ok) {
      // The body is read for the message and never logged wholesale: a
      // provider error quotes the request, and the request carries a sign-in
      // link.
      const body = await res.text().catch(() => '')
      throw new MailError(
        `The mail provider refused the message with ${res.status}. ${summarise(body)}`,
      )
    }
  }
}

/** Keeps messages instead of sending them, for tests. */
export class RecordingMailer implements Mailer {
  readonly sent: Message[] = []
  async send(message: Message): Promise<void> {
    this.sent.push(message)
  }
  /** The most recent message to an address, or undefined. */
  lastTo(address: string): Message | undefined {
    for (let i = this.sent.length - 1; i >= 0; i--) {
      if (this.sent[i]!.to.toLowerCase() === address.toLowerCase()) return this.sent[i]
    }
    return undefined
  }
}

/** The provider's error, shortened and stripped of anything that looks like a
 *  token, because this string ends up in a log line. */
function summarise(body: string): string {
  const oneLine = body.replace(/\s+/g, ' ').trim()
  const redacted = oneLine.replace(/[A-Za-z0-9_-]{24,}/g, '[redacted]')
  return redacted.length > 200 ? `${redacted.slice(0, 200)}...` : redacted
}
