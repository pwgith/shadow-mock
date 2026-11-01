// Serves an in-memory Postgres (PGlite) on a free local port, for the Go database tests (internal/store/postgres/pgtest).
// It prints one line of JSON, {"port": N}, once it accepts connections, and exits when its standard input closes,
// which is when the test binary that started it ends.
import { PGlite } from '@electric-sql/pglite'
import { PGLiteSocketServer } from '@electric-sql/pglite-socket'

const db = await PGlite.create()
const server = new PGLiteSocketServer({ db, port: 0, host: '127.0.0.1', maxConnections: 32 })
await server.start()
const port = Number(server.getServerConn().split(':').pop())
process.stdout.write(JSON.stringify({ port }) + '\n')

const stop = async () => {
  await server.stop().catch(() => {})
  await db.close().catch(() => {})
  process.exit(0)
}
process.stdin.on('end', stop)
process.stdin.on('close', stop)
process.stdin.resume()
process.on('SIGTERM', stop)
process.on('SIGINT', stop)
