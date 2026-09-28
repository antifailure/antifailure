import { test } from 'node:test';
import assert from 'node:assert/strict';
import { mkdtemp, readFile, readdir, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join } from 'node:path';
import { AgentReplay, requestKey, canonical } from '../src/index.ts';
import type { CaptureOptions, Incident, RequestIdentity } from '../src/index.ts';

const request: RequestIdentity = {kind:'model',name:'answer',version:'1',input:{question:'billing'},provider:'fixture',model:'v1',instructions:'Read the account',tools:[],settings:{temperature:0}};
async function fixture(t: {after(fn:()=>Promise<void>):void}, overrides: Partial<CaptureOptions> = {}) {
  const directory=await mkdtemp(join(tmpdir(),'af-capture-'));
  t.after(()=>rm(directory,{recursive:true,force:true}));
  const sdk=new AgentReplay({directory,project:'billing',service:'agent',commit:'a'.repeat(40),policyVersion:'1',content:['input','output','model:answer','tool:search','database:account'],now:()=>new Date('2026-09-27T00:00:00Z'),...overrides});
  return {sdk,directory,read:async(id='one')=>JSON.parse(await readFile(join(directory,id+'.json'),'utf8')) as Incident};
}

test('capture and strict replay reach the real wrapper without executing a fallback',async t=>{
  const {sdk,read}=await fixture(t);
  await sdk.run({id:1},()=>sdk.boundary(request,async()=>({recommendation:'charge'})),{runId:'one',identity:'synthetic',golden:'gv_test'});
  const incident=await read();
  assert.equal(incident.status,'complete');
  assert.equal(incident.exchanges[0]?.key,requestKey(request));
  const result=await sdk.replay({schemaVersion:1,input:incident.input!,clock:incident.observedAt,exchanges:incident.exchanges},async()=>sdk.boundary(request,async()=>{throw Error('fallback reached');}));
  assert.deepEqual(result.output,{recommendation:'charge'});
  assert.deepEqual(result.issues,[]);
  assert.equal(result.hits,1);
});

test('changed instructions, settings, model and schema miss even when the agent catches the error',async t=>{
  const {sdk,read}=await fixture(t);
  await sdk.run({},()=>sdk.boundary(request,async()=>true),{runId:'one',identity:'synthetic'});
  const incident=await read();
  for(const change of [{instructions:'Different'},{settings:{temperature:1}},{model:'v2'},{version:'2'}]){
    let calls=0;
    const result=await sdk.replay({schemaVersion:1,input:{},clock:incident.observedAt,exchanges:incident.exchanges},async()=>{
      try{await sdk.boundary({...request,...change},async()=>{calls++;return true;});}catch{}
      return 'looks fine';
    });
    assert.equal(calls,0);
    assert.match(result.issues.join(),/cassette_miss/);
  }
});

test('metadata mode retains hashes and never bodies',async t=>{
  const {sdk,directory,read}=await fixture(t,{content:[]});
  await sdk.run({private:'unique-user-input'},()=>sdk.boundary({...request,input:'unique-prompt'},async()=>'unique-answer'),{runId:'one'});
  const bytes=await readFile(join(directory,'one.json'),'utf8');
  for(const secret of ['unique-user-input','unique-prompt','unique-answer'])assert.ok(!bytes.includes(secret));
  const incident=await read();assert.equal(incident.input,undefined);assert.equal(incident.exchanges[0]?.request,null);
});

test('deny rules apply before persistence and transformed content cannot claim equivalent identity',async t=>{
  const {sdk,directory,read}=await fixture(t);
  const secret='Bearer '+ 'sensitive-value';
  await sdk.run({authorization:secret,password:'private-pass'},async()=>({password:'private-output'}),{runId:'one'});
  const body=await readFile(join(directory,'one.json'),'utf8');
  for(const value of [secret,'private-pass','private-output'])assert.ok(!body.includes(value));
  assert.match((await read()).issues.join(),/identity_transformed/);
});

test('a broken redactor or writer does not alter the host result or original exception',async t=>{
  const messages:string[]=[];
  const {sdk,read}=await fixture(t,{redact:()=>{throw Error('sensitive failure text');},onDiagnostic:reason=>messages.push(reason)});
  assert.equal(await sdk.run('hello',async()=>42,{runId:'one'}),42);
  assert.equal((await read()).status,'incomplete');
  const original=new Error('original application error');
  await assert.rejects(sdk.run({},async()=>{throw original;},{runId:'two'}),error=>error===original);
  const bad=new AgentReplay({project:'billing',service:'agent',commit:'a'.repeat(40),policyVersion:'1',directory:'/dev/null/unwritable',onDiagnostic:reason=>messages.push(reason)});
  assert.equal(await bad.run({},async()=>7,{runId:'three'}),7);
  assert.ok(messages.some(m=>m.includes('capture_write_failed')));
  assert.ok(!messages.join().includes('sensitive failure text'));
});

test('repeated identical requests consume recorded occurrences and never reuse the first answer',async t=>{
  const {sdk,read}=await fixture(t);
  await sdk.run({},async()=>{await sdk.boundary(request,async()=>1);return sdk.boundary(request,async()=>2);},{runId:'one',identity:'synthetic'});
  const incident=await read();
  const result=await sdk.replay({schemaVersion:1,input:{},clock:incident.observedAt,exchanges:incident.exchanges},async()=>{
    assert.equal(await sdk.boundary(request,async()=>99),1);
    return sdk.boundary(request,async()=>99);
  });
  assert.equal(result.output,2);assert.deepEqual(result.issues,[]);
});

test('parallel agent runs keep their context and files separate',async t=>{
  const {sdk,read,directory}=await fixture(t);
  await Promise.all(Array.from({length:20},(_,n)=>sdk.run({n},()=>sdk.boundary({...request,input:n},async()=>n),{runId:`run-${n}`,identity:'synthetic'})));
  assert.equal((await readdir(directory)).length,20);
  for(let n=0;n<20;n++){const capture=await read(`run-${n}`);assert.deepEqual(capture.input,{n});assert.equal(capture.output,n);assert.equal(capture.exchanges[0]?.response,n);}
});

test('concurrent boundaries and unawaited operations are explicitly incomplete',async t=>{
  const {sdk,read}=await fixture(t);
  let release!:()=>void;const barrier=new Promise<void>(resolve=>{release=resolve;});
  await sdk.run({},async()=>{const first=sdk.boundary(request,async()=>{await barrier;return 1;});const second=sdk.boundary(request,async()=>2);release();await first;return second;},{runId:'one'});
  assert.match((await read()).issues.join(),/concurrent_boundaries/);
});

test('database callbacks execute on replay and the SDK clock is fixed',async t=>{
  const {sdk,read}=await fixture(t);const database:RequestIdentity={kind:'database',name:'account',version:'1',input:{id:1}};
  await sdk.run({},()=>sdk.boundary(database,async()=>({status:'old'})),{runId:'one',identity:'synthetic'});
  const incident=await read();let invoked=0;
  const result=await sdk.replay({schemaVersion:1,input:{},clock:incident.observedAt,exchanges:incident.exchanges},async()=>{
    assert.equal(sdk.now().toISOString(),incident.observedAt);
    return sdk.boundary(database,async()=>{invoked++;return {status:'branch'};});
  });
  assert.equal(invoked,1);assert.deepEqual(result.output,{status:'branch'});assert.equal(result.hits,0);
});

test('canonical identity includes every setting and rejects non-JSON values',()=>{
  assert.equal(canonical({z:1,a:{b:true}}),'{'+'"a":{"b":true},"z":1}');
  assert.throws(()=>canonical({bad:undefined}));assert.throws(()=>canonical(NaN));
  assert.notEqual(requestKey(request),requestKey({...request,tools:[{name:'charge'}]}));
});

test('a sequential database capture cannot pass when a candidate starts concurrent operations',async t=>{
  const {sdk,read}=await fixture(t);
  const database:RequestIdentity={kind:'database',name:'account',version:'1',input:{id:1}};
  await sdk.run({},async()=>{await sdk.boundary(database,async()=>1);return sdk.boundary(database,async()=>2);},{runId:'one',identity:'synthetic'});
  const incident=await read();
  const result=await sdk.replay({schemaVersion:1,input:{},clock:incident.observedAt,exchanges:incident.exchanges},async()=>{
    await Promise.allSettled([sdk.boundary(database,async()=>1),sdk.boundary(database,async()=>2)]);
    return 'correct-looking output';
  });
  assert.ok(result.issues.includes('concurrent_boundaries'));
});

test('checkpoint before the run and checkpoint during the run preserve the same reference',async t=>{
  const {sdk,read}=await fixture(t);
  await sdk.run({},async()=>true,{runId:'before',golden:'gv-pinned'});
  await sdk.run({},async()=>{sdk.checkpoint('gv-pinned');return true;},{runId:'during'});
  assert.equal((await read('before')).golden,(await read('during')).golden);
  await sdk.run({},async()=>{sdk.checkpoint('gv-other');return true;},{runId:'changed',golden:'gv-pinned'});
  assert.ok((await read('changed')).issues.includes('checkpoint_changed'));
});

test('supplied usage metadata is retained without requiring content capture',async t=>{
  const {sdk,read}=await fixture(t,{content:[]});
  await sdk.run({},()=>sdk.boundary(request,async()=>true,()=>({inputTokens:10,outputTokens:4,costUSD:0.001})),{runId:'one'});
  assert.deepEqual((await read()).exchanges[0]?.usage,{inputTokens:10,outputTokens:4,costUSD:0.001});
});

test('invalid per-run identifiers do not take down the host and protected capture can fail closed',async t=>{
  const diagnostics:string[]=[];
  const {sdk,directory}=await fixture(t,{onDiagnostic:reason=>diagnostics.push(reason)});
  assert.equal(await sdk.run({},async()=>17,{runId:'../unsafe',traceId:'0'.repeat(32)}),17);
  const files=await readdir(directory);assert.equal(files.length,1);
  const incident=JSON.parse(await readFile(join(directory,files[0]),'utf8')) as Incident;
  assert.equal(incident.status,'incomplete');assert.ok(diagnostics.includes('trace_id_invalid'));
  const protectedCapture=new AgentReplay({project:'billing',service:'agent',commit:'a'.repeat(40),policyVersion:'1',directory:'/dev/null/unwritable',failClosed:true,onDiagnostic:()=>{}});
  await assert.rejects(protectedCapture.run({},async()=>true),/Protected capture/);
});
