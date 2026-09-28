// A real SDK, CLI, isolated application and Postgres. No production credentials.
import assert from 'node:assert/strict';
import { execFile, spawn } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, writeFile, readFile, copyFile, mkdir, readdir, rename } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const exec=promisify(execFile);
const here=dirname(fileURLToPath(import.meta.url));
const binary=resolve(process.env.AF_REPLAY_TEST_BIN ?? join(here,'../../../bin/af'));
const root=await mkdtemp(join(tmpdir(),'af-agent-replay-'));
const name='replay-'+root.split('-').at(-1).toLowerCase();
const evidence=resolve(process.env.AF_REPLAY_TEST_EVIDENCE ?? join(root,'evidence'));
await mkdir(evidence,{recursive:true});
console.log(`Replay fixture: ${root}`);
async function run(command,args,allowed=[0]){
  try{return await exec(command,args,{cwd:root,timeout:15*60*1000,maxBuffer:8*1024*1024,env:{...process.env,AF_TELEMETRY:'off',GIT_AUTHOR_NAME:'Synthetic fixture',GIT_AUTHOR_EMAIL:'fixture@example.test',GIT_COMMITTER_NAME:'Synthetic fixture',GIT_COMMITTER_EMAIL:'fixture@example.test'}});}
  catch(error){if(allowed.includes(error.code))return error;throw new Error(`${command} ${args.join(' ')}\n${error.stdout ?? ''}\n${error.stderr ?? ''}`,{cause:error});}
}
async function af(args,allowed=[0]){const result=await run(binary,[...args,'--output','json'],allowed);try{return JSON.parse(result.stdout);}catch{throw Error(`Unreadable CLI JSON: ${result.stdout}\n${result.stderr}`);}}
async function git(...args){return (await run('git',args)).stdout.trim();}
async function record(label,report){await writeFile(join(evidence,label+'.json'),JSON.stringify(report,null,2));console.log(`${label}: ${report.verdict ?? 'captured'}`);}
let captureUp=false;
try {
  await run('docker',['info','--format','{{.ServerVersion}}']);
  await git('init','--initial-branch=main','-q');
  for(const file of ['app.mjs','Dockerfile'])await copyFile(join(here,'../example',file),join(root,file));
  await copyFile(join(here,'../src/index.ts'),join(root,'sdk.ts'));
  await writeFile(join(root,'.gitignore'),'.antifailure/\nevidence/\nrevision.txt\n');
  await writeFile(join(root,'.dockerignore'),'.git\n.antifailure\nevidence\n');
  await writeFile(join(root,'project.txt'),name);
  await writeFile(join(root,'seed.sql'),"CREATE TABLE subscriptions (id integer PRIMARY KEY, status text NOT NULL, password text); INSERT INTO subscriptions VALUES (1, 'cancelled', NULL); CREATE TABLE visits (id serial PRIMARY KEY);\n");
  await writeFile(join(root,'decision.mjs'),"export const shouldCharge = status => status !== 'active';\n");
  await writeFile(join(root,'antifailure.yaml'),`version: 1\nname: ${name}\nservices:\n  - name: agent\n    kind: web\n    path: .\n    port: 3000\n    health_path: /health\n    build:\n      strategy: dockerfile\n      dockerfile: Dockerfile\ndatabase:\n  provider: docker\n  version: 17\n  seed: 'psql "$DATABASE_URL" -v ON_ERROR_STOP=1 -f seed.sql'\negress:\n  default: block\n`);
  await git('add','.');await git('commit','-qm','Synthetic billing failure');
  const baseline=await git('rev-parse','HEAD');
  console.log('Preparing verified golden');
  const golden=await af(['golden','refresh']);assert.equal(golden.verified,true);
  captureUp=true;const up=await af(['up','--branch','capture']);
  const response=await fetch(up.url+'/capture',{method:'POST',headers:{'content-type':'application/json'},body:JSON.stringify({input:{id:1},commit:baseline,golden:golden.version}),signal:AbortSignal.timeout(30000)});
  assert.equal(response.status,200);
  const capture=await response.json();assert.equal(capture.output.recommendation,'charge');
  assert.equal(capture.incident.commit,baseline);
  assert.equal(capture.incident.golden,golden.version);
  await writeFile(join(root,'capture.json'),JSON.stringify(capture.incident));
  await af(['down','--branch','capture']);captureUp=false;
  await af(['incident','import','capture.json']);
  const scenario=await af(['incident','save','billing-failure','--scenario','billing','--golden',golden.version,'--pointer','/recommendation','--original','"charge"','--expected','"review"','--table','subscriptions']);
  assert.equal(scenario.golden,golden.version);
  await record('capture',capture.incident);
  await writeFile(join(root,'decision.mjs'),"export const shouldCharge = status => status === 'active';\n");
  await git('add','decision.mjs');await git('commit','-qm','Fix cancelled subscription recommendation');
  const candidate=await git('rev-parse','HEAD');
  console.log('Reproducing the control and testing the fix');
  const fixed=await af(['replay','billing','--candidate',candidate]);await record('fixed',fixed);
  assert.equal(fixed.verdict,'PASS');assert.equal(fixed.baseline.assertion,true);assert.equal(fixed.candidate.assertion,true);
  assert.equal(fixed.baseline.tornDown,true);assert.equal(fixed.candidate.tornDown,true);assert.notEqual(fixed.baseline.envId,fixed.candidate.envId);
  assert.equal(fixed.baseline.response.output.visitId,1);assert.equal(fixed.candidate.response.output.visitId,1);assert.equal(fixed.databaseUnchanged,true);
  const regressed=await af(['replay','billing','--candidate',baseline],[8]);await record('regressed',regressed);assert.equal(regressed.verdict,'FAIL');
  const missing=await af(['replay','absent','--candidate',candidate],[7]);await record('missing',missing);assert.equal(missing.verdict,'INCONCLUSIVE');
  const app=await readFile(join(root,'app.mjs'),'utf8');
  await writeFile(join(root,'app.mjs'),app.replace('async function agent(sdk, input) {',`async function agent(sdk, input) {
    try { await sdk.boundary({kind:'tool',name:'uncaptured',version:'1',input:{}},async()=>true); } catch {}
  `));
  await git('add','app.mjs');await git('commit','-qm','Negative control with a caught cassette miss');
  const divergent=await git('rev-parse','HEAD');
  const diverged=await af(['replay','billing','--candidate',divergent],[7]);await record('diverged',diverged);assert.equal(diverged.verdict,'INCONCLUSIVE');assert.match(diverged.issues.join(),/cassette_miss|incomplete SDK evidence/);
  await writeFile(join(root,'app.mjs'),app.replace('  return {recommendation:',`  await exec('psql',[process.env.DATABASE_URL,'-At','-v','ON_ERROR_STOP=1','-c',"UPDATE subscriptions SET status='active', password='short-after'"]);
  return {recommendation:`));
  await git('add','app.mjs');await git('commit','-qm','Negative control with an unexpected database write');
  const corrupt=await af(['replay','billing','--candidate',await git('rev-parse','HEAD')],[8]);await record('database-write',corrupt);assert.equal(corrupt.verdict,'FAIL');assert.equal(corrupt.candidate.assertion,true);assert.equal(corrupt.databaseUnchanged,false);assert.ok(!JSON.stringify(corrupt).includes('short-after'),'database credentials never enter the returned or persisted report');
  await writeFile(join(root,'app.mjs'),app.replace('  return {recommendation:',`  await fetch('https://payments.example.test/charge',{method:'POST',signal:AbortSignal.timeout(5000)}).catch(()=>{});
  return {recommendation:`));
  await git('add','app.mjs');await git('commit','-qm','Negative control with an unwrapped external call');
  const escaped=await af(['replay','billing','--candidate',await git('rev-parse','HEAD')],[7]);await record('blocked-egress',escaped);assert.equal(escaped.verdict,'INCONCLUSIVE');assert.match(escaped.issues.join(),/unrecorded external request refused/);
  await writeFile(join(root,'app.mjs'),app);await git('add','app.mjs');await git('commit','-qm','Restore the recorded boundary contract');
  await writeFile(join(root,'suite.json'),JSON.stringify({schemaVersion:1,scenarios:['billing']}));
  const suite=await af(['eval','run','suite.json','--candidate',candidate]);assert.equal(suite[0].verdict,'PASS');await record('suite',suite);
  const parallel=await Promise.all([af(['replay','billing','--candidate',candidate]),af(['replay','billing','--candidate',candidate])]);
  const environments=new Set();for(const report of parallel){assert.equal(report.verdict,'PASS');assert.equal(report.baseline.response.output.visitId,1);assert.equal(report.candidate.response.output.visitId,1);environments.add(report.baseline.envId);environments.add(report.candidate.envId);}
  assert.equal(environments.size,4);await record('concurrent',parallel);
  const blob=join(root,'.antifailure/replay/blobs',scenario.incidentRef+'.json');
  await rename(blob,blob+'.retained');
  try { const absent=await af(['replay','billing','--candidate',candidate],[7]);await record('missing-cassette',absent);assert.equal(absent.verdict,'INCONCLUSIVE'); }
  finally {await rename(blob+'.retained',blob);}
  console.log('Interrupting a real replay and recovering without its incident blob');
  const attempts=join(root,'.antifailure/replay/attempts');
  const previous=new Set(await readdir(attempts));
  const child=spawn(binary,['replay','billing','--candidate',candidate,'--output','json'],{cwd:root,stdio:['ignore','ignore','pipe']});
  let childError='';child.stderr.on('data',chunk=>{childError+=chunk;});
  const exited=new Promise(resolve=>child.once('exit',(code,signal)=>resolve({code,signal})));
  let interrupted;
  try {
    const deadline=Date.now()+10*60*1000;
    while(Date.now()<deadline){
      if(child.exitCode!==null)throw Error(`Replay exited before interruption: ${childError}`);
      for(const name of await readdir(attempts)){
        if(previous.has(name)||!name.endsWith('.json'))continue;
        const attempt=JSON.parse(await readFile(join(attempts,name),'utf8'));
        if(attempt.state==='candidate')interrupted=attempt;
      }
      if(interrupted)break;
      await new Promise(resolve=>setTimeout(resolve,100));
    }
    assert.ok(interrupted,'the real baseline reached candidate startup');
    child.kill('SIGKILL');await exited;
    await rename(blob,blob+'.retained');
    try {
      const recovered=await af(['replay','recover',interrupted.id]);await record('recovered',recovered);
      assert.equal(recovered.verdict,'INCONCLUSIVE');assert.equal(recovered.baseline.tornDown,true);assert.equal(recovered.candidate.tornDown,true);
    } finally {await rename(blob+'.retained',blob);}
  }finally{if(child.exitCode===null && child.signalCode===null){child.kill('SIGTERM');await exited;}}
  await af(['replay','retire','billing','--reason','Synthetic conformance run finished']);
  await af(['replay','retire','billing','--reason','Synthetic conformance run finished']);
  const refusedImport=await af(['incident','import','capture.json'],[3]);assert.ok(JSON.stringify(refusedImport).includes('retired'));
  const retired=await af(['replay','billing','--candidate',candidate],[7]);assert.equal(retired.verdict,'INCONCLUSIVE');assert.ok(retired.issues.includes('scenario_retired'));await record('retired',retired);
  console.log(`Evidence: ${evidence}`);
}finally{
  if(captureUp)await af(['down','--branch','capture']).catch(error=>console.error('Capture teardown failed:',error.message));
}
