// FM-ROUTERa (L48): `app/` is routes only. Expo Router imports every file
// under app/ through require.context at bundle time — a *.test.tsx there is
// not a route, but it is imported anyway, and @testing-library/react-native
// reaches Node built-ins (require("console"), require("util")) the native
// runtime does not have. LIVE failure this guard exists for: `expo start`
// died 153s in with
//
//   The package at "node_modules/@testing-library/react-native/dist/..."
//   attempted to import the Node standard library module "console".
//   ... app/menu/help.test.tsx | import "@testing-library/react-native"
//   ... app (require.context)
//
// Jest cannot catch this — it runs in Node, where console/util exist — so a
// green `npm test` and a dead Metro shipped on the same commit. This guard
// is the gate that sees what the bundler sees.
import { readdirSync, statSync } from 'node:fs'
import { join, relative } from 'node:path'

const ROOT = join(__dirname, '..', '..')

function findRouteTreeTests(dir: string, out: string[] = []): string[] {
  for (const entry of readdirSync(dir)) {
    const p = join(dir, entry)
    if (statSync(p).isDirectory()) {
      findRouteTreeTests(p, out)
    } else if (/\.(test|spec)\.[jt]sx?$/.test(entry)) {
      out.push(relative(ROOT, p))
    }
  }
  return out
}

it('app/ holds routes only — no test files inside the route tree', () => {
  const offenders = findRouteTreeTests(join(ROOT, 'app'))
  // RED pre-fix: lists the live offenders (menu/help.test.tsx,
  // (tabs)/build-export.test.tsx, (tabs)/device.test.tsx). GREEN: empty —
  // every test sits outside app/, where Metro can never bundle it.
  expect(offenders).toEqual([])
})
