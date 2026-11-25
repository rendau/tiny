// Проверка сборки: ровно один JS и один CSS, без динамических подгрузок.
import { readdirSync, statSync } from 'node:fs'
import { extname, join } from 'node:path'

const dir = process.argv[2] ?? '.output/public'
const files = readdirSync(dir, { recursive: true, withFileTypes: true })
  .filter(e => e.isFile())
  .map(e => join(e.parentPath, e.name))

const js = files.filter(f => extname(f) === '.js' && !f.endsWith('config.js'))
const css = files.filter(f => extname(f) === '.css')

const problems = []
if (js.length !== 1)
  problems.push(`ожидался 1 JS-файл, найдено ${js.length}: ${js.join(', ')}`)
if (css.length !== 1)
  problems.push(`ожидался 1 CSS-файл, найдено ${css.length}: ${css.join(', ')}`)

// Порог размера JS (несжатый). Заметный рост сверх — повод посмотреть, что попало в бандл.
const MAX_JS_BYTES = 1_300_000
if (js.length === 1 && statSync(js[0]).size > MAX_JS_BYTES)
  problems.push(`JS больше ${MAX_JS_BYTES} байт: ${statSync(js[0]).size}`)

if (problems.length) {
  console.error(problems.join('\n'))
  process.exit(1)
}
console.log(`bundle ok: ${js[0]} (${statSync(js[0]).size} байт), ${css[0]}`)
