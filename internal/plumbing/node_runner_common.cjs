'use strict';
const fs = require('node:fs');
const path = require('node:path');
const crypto = require('node:crypto');
const {createRequire} = require('node:module');
const {pathToFileURL} = require('node:url');
const root = process.cwd();
const localRequire = createRequire(path.join(root, 'package.json'));
const read = fs.readFileSync.bind(fs);
const write = fs.writeSync.bind(fs);
const stringify = JSON.stringify.bind(JSON);
const createHmac = crypto.createHmac.bind(crypto);
const hmacUpdate = Function.call.bind(crypto.Hmac.prototype.update);
const hmacDigest = Function.call.bind(crypto.Hmac.prototype.digest);
const from = Buffer.from.bind(Buffer);
const slice = Function.call.bind(String.prototype.slice);
const clearKey = Function.call.bind(Buffer.prototype.fill);
const digest = bytes => 'sha256:' + crypto.createHash('sha256').update(bytes).digest('hex');

function regular(file) {
  file = path.resolve(file);
  if (!file.startsWith(root + path.sep)) throw new Error('file escapes captured part');
  for (let p = file; p !== root; p = path.dirname(p)) {
    const stat = fs.lstatSync(p);
    if (stat.isSymbolicLink() || (p === file ? !stat.isFile() || stat.nlink !== 1 : !stat.isDirectory())) {
      throw new Error('source or dependency is not a confined regular file');
    }
  }
  return file;
}

let lock;
function lockedFile(file) {
  if (!lock) lock = JSON.parse(read(regular(path.join(root, 'package-lock.json')), 'utf8'));
  file = regular(file);
  const relative = path.relative(root, file).split(path.sep).join('/');
  if (!relative.startsWith('node_modules/') || /\/(?:\.cache|\.vite)\//.test(relative)) {
    throw new Error('framework or transformer did not resolve from prepared dependencies');
  }
  let directory = path.dirname(file);
  while (!fs.existsSync(path.join(directory, 'package.json'))) {
    if (directory === root) throw new Error('dependency has no package manifest');
    directory = path.dirname(directory);
  }
  const key = path.relative(root, directory).split(path.sep).join('/');
  const manifest = JSON.parse(read(regular(path.join(directory, 'package.json')), 'utf8'));
  const pinned = lock.packages?.[key];
  if (!pinned?.integrity || pinned.link || pinned.version !== manifest.version) {
    throw new Error('resolved dependency differs from captured npm lock: ' + key);
  }
  return file;
}
function resolve(name) { return lockedFile(localRequire.resolve(name)); }
function requireLocked(name) { return localRequire(resolve(name)); }
function importLocked(name) { return import(pathToFileURL(resolve(name)).href); }

function open(input, actor, endpoint) {
  // Only the parent gets a credential. Consume and close its anonymous bootstrap
  // descriptor before starting isolated framework workers. Neither
  // argv, env, a staged file nor this module's exports retains the key or signer.
  let key;
  if (actor === 'parent') {
    key = Buffer.alloc(32);
    try {
      if (fs.readSync(4, key, 0, key.length, null) !== key.length) throw new Error('missing parent credential');
    } finally { fs.closeSync(4); }
  } else if (actor !== 'worker') throw new Error('unknown Node observer');
  Object.setPrototypeOf(input.Binding, null);
  if (!['vitest', 'jest'].includes(input.Binding.Framework)) throw new Error('unknown Node framework');
  const file = regular(path.join(root, input.File));
  const recheck = () => {
    if (digest(read(regular(file))) !== input.Binding.SourceDigest) throw new Error('acceptance source changed');
  };
  recheck();
  const fd = fs.openSync(endpoint, 'w');
  let seq = 0;
  const emit = (event, extra = {}) => {
    let record = stringify({__proto__: null, Binding: input.Binding, Actor: actor, Seq: ++seq, Event: event, ...extra});
    if (key) {
      const mac = hmacDigest(hmacUpdate(createHmac('sha256', key), record), 'hex');
      record = slice(record, 0, -1) + ',"MAC":"' + mac + '"}';
    }
    const bytes = from(record + '\n');
    // One pipe write is atomic across the parent and worker observers.
    if (bytes.length > 4096) throw new Error('Node evidence exceeds atomic frame size');
    if (write(fd, bytes) !== bytes.length) throw new Error('short Node evidence write');
  };
  // Prepared Node execution is Linux-only. Forked framework workers can open
  // this event descriptor without inheriting the consumed credential or sharing
  // the controller's heap. Their writes still have no terminal authentication.
  return {input, file, emit, recheck, endpoint: '/proc/' + process.pid + '/fd/' + fd,
    close: () => { if (key) clearKey(key, 0); fs.closeSync(fd); }};
}

function options(input) {
  const result = {};
  for (let i = input.Binding.Framework === 'vitest' ? 2 : 1; i < input.Args.length - 1; i++) {
    const [flag, ...tail] = input.Args[i].split('=');
    if (flag === '--runInBand' || flag === '--runTestsByPath') continue;
    const value = tail.length ? tail.join('=') : input.Args[++i];
    if (flag === '--config') result.config = value;
    else if (flag === '--testTimeout') result.testTimeout = Number(value);
    else if (flag === '-t' || flag === '--testNamePattern') result.testNamePattern = value;
    else throw new Error('unexpected narrowed Node argument');
  }
  if (!result.testNamePattern) throw new Error('missing exact title filter');
  return result;
}

// Configuration is data, never an import into the authenticated observer. The
// locked parser accepts only a literal CommonJS assignment or default export.
function literalConfig(file) {
  const ts = requireLocked('typescript');
  const source = ts.createSourceFile(file, read(file, 'utf8'), ts.ScriptTarget.Latest, true, ts.ScriptKind.JS);
  const refuse = () => { throw new Error('framework configuration must contain only literal data'); };
  if (source.parseDiagnostics.length || source.statements.length !== 1) return refuse();
  const statement = source.statements[0];
  let value;
  if (ts.isExportAssignment(statement) && !statement.isExportEquals) value = statement.expression;
  else if (ts.isExpressionStatement(statement) && ts.isBinaryExpression(statement.expression)) {
    const assignment = statement.expression;
    const left = assignment.left;
    if (assignment.operatorToken.kind !== ts.SyntaxKind.EqualsToken || !ts.isPropertyAccessExpression(left) ||
        !ts.isIdentifier(left.expression) || left.expression.text !== 'module' || left.name.text !== 'exports') return refuse();
    value = assignment.right;
  } else return refuse();
  function data(node) {
    if (ts.isParenthesizedExpression(node)) return data(node.expression);
    if (ts.isStringLiteral(node) || ts.isNoSubstitutionTemplateLiteral(node)) return node.text;
    if (ts.isNumericLiteral(node)) return Number(node.text);
    if (node.kind === ts.SyntaxKind.TrueKeyword) return true;
    if (node.kind === ts.SyntaxKind.FalseKeyword) return false;
    if (node.kind === ts.SyntaxKind.NullKeyword) return null;
    if (ts.isArrayLiteralExpression(node)) return node.elements.map(data);
    if (ts.isObjectLiteralExpression(node)) {
      const result = Object.create(null);
      for (const property of node.properties) {
        if (!ts.isPropertyAssignment(property) ||
            !ts.isIdentifier(property.name) && !ts.isStringLiteral(property.name)) return refuse();
        const name = property.name.text;
        if (['__proto__', 'constructor', 'prototype'].includes(name) || Object.hasOwn(result, name)) return refuse();
        result[name] = data(property.initializer);
      }
      return result;
    }
    return refuse();
  }
  return data(value);
}

async function config(framework, selected) {
  let file = selected;
  if (!file) {
    const found = ['cjs', 'js', 'mjs', 'json', 'ts', 'mts'].map(ext => framework + '.config.' + ext).filter(p => fs.existsSync(path.join(root, p)));
    if (found.length > 1) throw new Error('ambiguous framework configuration');
    file = found[0];
  }
  if (!file) {
    if (framework === 'jest') {
      const manifest = JSON.parse(read(regular(path.join(root, 'package.json')), 'utf8'));
      if (manifest.jest) {
        if (typeof manifest.jest !== 'object' || Array.isArray(manifest.jest)) throw new Error('invalid Jest package configuration');
        return manifest.jest;
      }
    } else if (['js', 'cjs', 'mjs', 'ts', 'mts'].some(ext => fs.existsSync(path.join(root, 'vite.config.' + ext)))) {
      throw new Error('acceptance requires an explicit supported Vitest configuration');
    }
    return {};
  }
  file = regular(path.join(root, file));
  let value;
  if (file.endsWith('.json')) value = JSON.parse(read(file, 'utf8'));
  else if (/\.(?:cjs|js|mjs)$/.test(file)) value = literalConfig(file);
  else throw new Error('acceptance configuration requires regular JS or JSON');
  if (!value || typeof value !== 'object' || Array.isArray(value)) throw new Error('framework configuration must export an object');
  return value;
}

function onlyKeys(value, keys, description) {
  for (const key of Object.keys(value)) if (!keys.includes(key)) throw new Error('unsupported ' + description + ' option ' + key);
}

function assertion(error) {
  return Boolean(error && (error.matcherResult && typeof error.matcherResult.pass === 'boolean' ||
    error.name === 'AssertionError' && ('actual' in error || 'expected' in error || error.code === 'ERR_ASSERTION')));
}

// TypeScript supplies structured diagnostics and syntax, never console parsing.
// Attribution is intentionally narrow: one named relative import, used directly
// by the first expression of the exact, statically named test. The production
// module must compile and have only declarations whose loading cannot execute
// code. No production bytes are evaluated in the authenticated observer.
function analyze(input, policy = {}) {
  let ts;
  try { ts = requireLocked('typescript'); }
  catch (e) {
    if (/\.(?:ts|tsx|mts|cts)$/.test(input.File)) throw e;
    return {proposed: null};
  }
  const file = regular(path.join(root, input.File));
  const converted = ts.convertCompilerOptionsFromJson(policy.compiler || {}, root);
  if (converted.errors.length) throw new Error('invalid captured TypeScript compiler settings');
  const types = ['node'];
  if (input.Binding.Framework === 'vitest' && policy.globals) types.push('vitest/globals');
  const settings = {target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.CommonJS,
    moduleResolution: ts.ModuleResolutionKind.Node10, jsx: ts.JsxEmit.Preserve,
    esModuleInterop: true, skipLibCheck: true, types, allowJs: true, checkJs: true,
    ...converted.options, noEmit: true};
  // Explicit ambient type packages must also come from the locked preparation.
  // Candidate declarations may never stand in for framework globals.
  for (const type of settings.types || []) {
    const resolved = ts.resolveTypeReferenceDirective(type, file, settings, ts.sys).resolvedTypeReferenceDirective;
    if (!resolved) throw new Error('captured TypeScript types are unavailable: ' + type);
    lockedFile(resolved.resolvedFileName);
  }
  const program = ts.createProgram([file], settings);
  const checker = program.getTypeChecker();
  const source = program.getSourceFile(file);
  const diagnostics = ts.getPreEmitDiagnostics(program);
  if (!diagnostics.length) return {proposed: null};
  const refusal = () => { throw new Error('selected source has unattributed transform, import or type diagnostics'); };
  if (diagnostics.length !== 1) return refusal();
  const diagnostic = diagnostics[0];
  if (diagnostic.code !== 2305 || diagnostic.file?.fileName !== file || !source) return refusal();
  let imported;
  for (const statement of source.statements) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier) || !statement.moduleSpecifier.text.startsWith('.')) continue;
    for (const element of statement.importClause?.namedBindings?.elements || []) {
      if (!element.propertyName && diagnostic.start >= element.getStart(source) && diagnostic.start < element.end) {
        imported = {symbol: element.name.text, specifier: statement.moduleSpecifier.text,
          binding: checker.getSymbolAtLocation(element.name), declaration: statement};
      }
    }
  }
  if (!imported) return refusal();
  const framework = input.Binding.Framework === 'jest' ? '@jest/globals' : 'vitest';
  function frameworkImport(statement) {
    if (!ts.isImportDeclaration(statement) || !ts.isStringLiteral(statement.moduleSpecifier) ||
        statement.moduleSpecifier.text !== framework || statement.importClause?.isTypeOnly) return false;
    // Resolve both runtime and type declarations from the prepared package.
    // Ambient/local declarations of a familiar module name are not authority.
    resolve(framework);
    const resolved = ts.resolveModuleName(framework, file, settings, ts.sys).resolvedModule?.resolvedFileName;
    if (!resolved || !resolved.startsWith(path.join(root, 'node_modules', framework) + path.sep)) return false;
    lockedFile(resolved);
    const symbol = checker.getSymbolAtLocation(statement.moduleSpecifier);
    return symbol?.declarations?.length && symbol.declarations.every(d => d.getSourceFile().fileName === resolved);
  }
  function frameworkBinding(identifier, roles) {
    if (!ts.isIdentifier(identifier)) return '';
    const symbol = checker.getSymbolAtLocation(identifier);
    if (symbol?.declarations?.length !== 1) return '';
    const declaration = symbol.declarations[0];
    if (!ts.isImportSpecifier(declaration) || declaration.isTypeOnly ||
        !frameworkImport(declaration.parent.parent.parent)) return '';
    const name = (declaration.propertyName || declaration.name).text;
    if (!roles.includes(name)) return '';
    const target = checker.getAliasedSymbol(symbol);
    if (!target?.declarations?.length) return '';
    for (const d of target.declarations) lockedFile(d.getSourceFile().fileName);
    return name;
  }
  const tests = [];
  function declarations(statements, prefix) {
    for (const statement of statements) {
      if (ts.isImportDeclaration(statement)) {
        if (statement !== imported.declaration && !frameworkImport(statement)) return false;
        continue;
      }
      if (!ts.isExpressionStatement(statement) || !ts.isCallExpression(statement.expression)) return false;
      const call = statement.expression;
      const role = frameworkBinding(call.expression, ['test', 'it', 'describe']);
      if (!role ||
          !ts.isStringLiteral(call.arguments[0]) || call.arguments.length !== 2) return false;
      const callback = call.arguments[1];
      if (!ts.isArrowFunction(callback) && !ts.isFunctionExpression(callback) || callback.parameters.length ||
          callback.asteriskToken || callback.modifiers?.some(m => m.kind === ts.SyntaxKind.AsyncKeyword)) return false;
      const title = [...prefix, call.arguments[0].text];
      if (role === 'describe') {
        if (!ts.isBlock(callback.body) || !declarations(callback.body.statements, title)) return false;
      } else tests.push({title: title.join(' '), callback});
    }
    return true;
  }
  if (!declarations(source.statements, [])) return refusal();
  const selected = tests.filter(t => t.title === input.Binding.Selector);
  if (selected.length !== 1) return refusal();
  const body = selected[0].callback.body;
  const expression = ts.isBlock(body) ? body.statements.length === 1 && ts.isExpressionStatement(body.statements[0]) && body.statements[0].expression : body;
  // Admit expect(api()).matcher(literal) only. There is no conditional, early
  // return, shadowing, helper call or earlier expression to obscure attribution.
  if (!expression || !ts.isCallExpression(expression) || !ts.isPropertyAccessExpression(expression.expression)) return refusal();
  const expect = expression.expression.expression;
  if (!ts.isCallExpression(expect) || !frameworkBinding(expect.expression, ['expect']) || expect.arguments.length !== 1) return refusal();
  const use = expect.arguments[0];
  if (!ts.isCallExpression(use) || !ts.isIdentifier(use.expression) || use.expression.text !== imported.symbol ||
      checker.getSymbolAtLocation(use.expression) !== imported.binding || use.arguments.length) return refusal();
  if (expression.arguments.some(a => !ts.isLiteralExpression(a))) return refusal();
  const resolved = ts.resolveModuleName(imported.specifier, file, settings, ts.sys).resolvedModule?.resolvedFileName;
  if (!resolved || resolved.includes('/node_modules/') || /(?:\.test|\.spec|\.d)\.[cm]?[jt]sx?$/.test(resolved)) return refusal();
  const product = regular(resolved);
  const production = ts.createProgram([product], settings);
  if (ts.getPreEmitDiagnostics(production).length) return refusal();
  const ast = production.getSourceFile(product);
  for (const statement of ast.statements) {
    if (statement.modifiers?.some(m => m.kind !== ts.SyntaxKind.ExportKeyword && m.kind !== ts.SyntaxKind.AsyncKeyword)) return refusal();
    if (ts.isFunctionDeclaration(statement) && statement.name && statement.body) continue;
    if (!ts.isVariableStatement(statement) || statement.declarationList.declarations.some(d =>
        !ts.isIdentifier(d.name) || !d.initializer || !ts.isLiteralExpression(d.initializer))) return refusal();
  }
  const module = production.getTypeChecker().getSymbolAtLocation(ast);
  if (!module || production.getTypeChecker().getExportsOfModule(module).some(s => s.name === imported.symbol)) return refusal();
  return {proposed: {symbol: imported.symbol, specifier: imported.specifier, file, code: diagnostic.code,
    reason: 'selected test directly calls missing local production export ' + imported.symbol}};
}

function apiError(error, proposed) {
  return Boolean(proposed && error?.name === 'TypeError' && String(error.stack).includes(proposed.file) &&
    String(error.message).includes(proposed.symbol) && String(error.message).includes('is not a function'));
}

function observer(session, proposed) {
  let collected = false, called = false, ended = false, done = false;
  let outcome, reason;
  return {
    collection(tests) {
      session.recheck();
      if (collected) throw new Error('duplicate Node collection');
      collected = true;
      const selected = tests.filter(t => t.title === session.input.Binding.Selector);
      if (selected.length !== 1 || selected[0].disabled) throw new Error('selection is absent, duplicate, skipped, todo or expected failure');
      session.emit('collection', {IDs: [session.input.Binding.Selector]});
    },
    start() {
      if (!collected || called) throw new Error('selected callback did not start uniquely');
      called = true;
      session.emit('body_start');
    },
    end(error) {
      if (!called || ended) throw new Error('selected callback did not finish uniquely');
      ended = true;
      outcome = !error ? 'passed' : assertion(error) ? 'assertion' : apiError(error, proposed) ? 'proposed_api' : 'error';
      reason = outcome === 'proposed_api' ? proposed.reason : error ? String(error.message).slice(0, 500) : 'selected callback returned normally';
      session.emit('body_end', {Outcome: outcome, Reason: reason});
    },
    finish(valid) {
      session.recheck();
      if (!collected || !called || !ended || done || !valid) throw new Error('selected call or hooks did not complete normally');
      done = true;
      session.emit('hooks', {Outcome: 'passed'});
      session.emit('suite_end', {Outcome: 'passed'});
    },
    get outcome() { return outcome; },
    get done() { return done; },
  };
}

module.exports = Object.freeze({regular, resolve, requireLocked, importLocked, open, options, config, onlyKeys, analyze, assertion, apiError, observer, root});
