const vm = require('node:vm');
const { format } = require('node:util');

let pending = Buffer.alloc(0);
let chain = Promise.resolve();
let buffer = { text: '' };

function capture(...args) {
  buffer.text += format(...args) + '\n';
}

const sandboxConsole = {
  log: capture,
  info: capture,
  debug: capture,
  trace: capture,
  dir: capture,
  table: capture,
  warn: capture,
  error: capture,
};

const sandbox = {
  console: sandboxConsole,
  setTimeout,
  clearTimeout,
  setInterval,
  clearInterval,
  setImmediate,
  clearImmediate,
  Buffer,
  URL,
  URLSearchParams,
  TextEncoder,
  TextDecoder,
  process,
  require,
};

const context = vm.createContext(sandbox);

function isTopLevelAwaitError(err) {
  return err instanceof SyntaxError && /await is only valid/.test(err.message);
}

function skipString(code, i) {
  const quote = code[i];
  i++;
  while (i < code.length) {
    if (code[i] === '\\') { i += 2; continue; }
    if (code[i] === quote) return i + 1;
    i++;
  }
  return code.length;
}

function lastTopLevelStatement(code) {
  let depth = 0;
  let cut = -1;
  let i = 0;
  while (i < code.length) {
    const c = code[i];
    if (c === '"' || c === "'" || c === '`') { i = skipString(code, i); continue; }
    if (c === '/' && code[i + 1] === '/') { while (i < code.length && code[i] !== '\n') i++; continue; }
    if (c === '/' && code[i + 1] === '*') { const end = code.indexOf('*/', i + 2); i = end < 0 ? code.length : end + 2; continue; }
    if (c === '(' || c === '[' || c === '{') depth++;
    else if (c === ')' || c === ']' || c === '}') depth--;
    else if (depth === 0 && c === ';') cut = i;
    i++;
  }
  const raw = code.slice(cut + 1);
  const start = raw.length - raw.trimStart().length;
  return { start: cut + 1 + start, text: raw.trim().replace(/;+$/, '') };
}

const DECLARATION_KEYWORDS = /^(?:let|const|var|function|class|if|for|while|do|switch|try|throw|return|import|export|break|continue)\b/;

function awaitWrapped(code) {
  const last = lastTopLevelStatement(code);
  if (last.text && !DECLARATION_KEYWORDS.test(last.text)) {
    const prefix = code.slice(0, last.start);
    return { script: '(async () => {' + prefix + ' return (' + last.text + '); })()', returns: true };
  }
  return { script: '(async () => {' + code + '\n})()', returns: false };
}

function stringify(value) {
  if (value === undefined) return '';
  return String(value);
}

function compileAwaitFallback(code) {
  const wrapped = awaitWrapped(code);
  if (wrapped.returns) {
    try {
      return new vm.Script(wrapped.script, { filename: 'eval.js' });
    } catch (err) {
      if (!(err instanceof SyntaxError)) throw err;
    }
  }
  return new vm.Script(wrapped.script, { filename: 'eval.js' });
}

async function execute(code) {
  buffer = { text: '' };
  const current = buffer;
  try {
    let script;
    try {
      script = new vm.Script(code, { filename: 'eval.js' });
    } catch (err) {
      if (!isTopLevelAwaitError(err)) throw err;
      script = compileAwaitFallback(code);
    }
    let result = script.runInContext(context);
    if (result && typeof result.then === 'function') {
      result = await result;
    }
    return { ok: true, stdout: current.text, value: stringify(result), error: '' };
  } catch (err) {
    const message = err && err.stack ? err.stack : String(err);
    return { ok: false, stdout: current.text, value: '', error: message };
  }
}

function emit(frameId, payload) {
  const raw = Buffer.from(JSON.stringify(payload), 'utf8');
  const header = Buffer.from(frameId + ' ' + raw.length + '\n', 'ascii');
  process.stdout.write(Buffer.concat([header, raw]));
}

function drainFrames() {
  for (;;) {
    const newline = pending.indexOf(0x0a);
    if (newline < 0) return;
    const parts = pending.subarray(0, newline).toString('utf8').trim().split(/\s+/);
    if (parts.length !== 2 || !/^\d+$/.test(parts[1])) {
      pending = pending.subarray(newline + 1);
      continue;
    }
    const size = Number(parts[1]);
    if (pending.length < newline + 1 + size) return;
    const code = pending.subarray(newline + 1, newline + 1 + size).toString('utf8');
    pending = pending.subarray(newline + 1 + size);
    const frameId = parts[0];
    chain = chain.then(async () => {
      const payload = await execute(code);
      emit(frameId, payload);
    });
  }
}

process.stdin.on('data', chunk => {
  pending = Buffer.concat([pending, chunk]);
  drainFrames();
});

process.stdin.on('end', () => {
  chain.finally(() => process.exit(0));
});
