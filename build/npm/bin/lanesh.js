#!/usr/bin/env node
import { spawn } from 'node:child_process';
import { readFileSync } from 'node:fs';

import { ensureBinary } from '../lib/launcher.js';

const { version } = JSON.parse(readFileSync(new URL('../package.json', import.meta.url), 'utf8'));

let bin;
try {
  bin = await ensureBinary(version);
} catch (err) {
  process.stderr.write(`lanesh: ${err.message}\n`);
  process.exit(1);
}

const child = spawn(bin, process.argv.slice(2), { stdio: 'inherit', windowsHide: true });

for (const signal of ['SIGINT', 'SIGTERM', 'SIGHUP']) {
  process.on(signal, () => child.kill(signal));
}

child.on('error', (err) => {
  process.stderr.write(`lanesh: failed to start ${bin}: ${err.message}\n`);
  process.exit(1);
});

child.on('exit', (code, signal) => {
  if (signal) {
    process.kill(process.pid, signal);
    return;
  }
  process.exit(code ?? 1);
});
