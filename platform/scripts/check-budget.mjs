import {readFileSync} from 'node:fs'
import {gzipSync} from 'node:zlib'
import assert from 'node:assert/strict'
const root=new URL('../web/dist/',import.meta.url)
const manifest=JSON.parse(readFileSync(new URL('.vite/manifest.json',root),'utf8'))
const entries=Object.entries(manifest)
const initial=entries.find(([,v])=>v.isEntry)?.[0]
const publicEntry=entries.find(([k])=>k.endsWith('/Public.vue'))?.[0]
assert(initial&&publicEntry,'Entry and public route must exist')
const files=new Set(),visited=new Set()
function collect(key){if(visited.has(key))return;visited.add(key);const item=manifest[key];files.add(item.file);for(const css of item.css||[])files.add(css);for(const dep of item.imports||[])collect(dep)}
collect(initial);collect(publicEntry)
let js=0,css=0
for(const file of files){assert(!/Admin|quill|primevue/i.test(file),'Admin dependency entered public bundle');const bytes=gzipSync(readFileSync(new URL(file,root))).length;if(file.endsWith('.js'))js+=bytes;if(file.endsWith('.css'))css+=bytes}
console.log(`Public route gzip: JS ${(js/1024).toFixed(1)} KiB; CSS ${(css/1024).toFixed(1)} KiB`)
assert(js<=200*1024,'Public JS exceeds 200 KiB');assert(css<=100*1024,'Public CSS exceeds 100 KiB')
