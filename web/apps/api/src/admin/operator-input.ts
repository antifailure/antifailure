import { emitKeypressEvents } from 'node:readline'
import type { ReadStream, WriteStream } from 'node:tty'

/** Read from the terminal without ever echoing a password, including on paste. */
export async function operatorInput(
  label: string,
  secret: boolean,
  input: ReadStream = process.stdin,
  output: WriteStream = process.stderr,
): Promise<string> {
  if (!input.isTTY || !output.isTTY) {
    throw new Error('Interactive setup needs a terminal. For automation, pass email and name and pipe the password on standard input.')
  }
  emitKeypressEvents(input)
  const wasRaw = input.isRaw
  input.setRawMode(true)
  input.resume()
  output.write(label)
  return await new Promise<string>((resolve, reject) => {
    let value = ''
    const finish = (error?: Error) => {
      input.removeListener('keypress', keypress)
      input.removeListener('end', ended)
      input.setRawMode(wasRaw)
      input.pause()
      output.write('\n')
      if (error) reject(error)
      else resolve(value)
    }
    const ended = () => finish(new Error('Setup cancelled before the answer was complete.'))
    const keypress = (text: string | undefined, key: { name?: string; ctrl?: boolean; meta?: boolean }) => {
      if (key.ctrl && (key.name === 'c' || key.name === 'd')) {
        finish(new Error('Setup cancelled.'))
      } else if (key.name === 'return' || key.name === 'enter') {
        finish()
      } else if (key.name === 'backspace') {
        if (value) {
          value = Array.from(value).slice(0, -1).join('')
          if (!secret) output.write('\b \b')
        }
      } else if (text && !key.ctrl && !key.meta && !/[\u0000-\u001f\u007f]/.test(text)) {
        if (value.length + text.length > 512) {
          finish(new Error('That answer is too long. Setup accepts at most 512 characters.'))
          return
        }
        value += text
        if (!secret) output.write(text)
      }
    }
    input.on('keypress', keypress)
    input.once('end', ended)
  })
}
