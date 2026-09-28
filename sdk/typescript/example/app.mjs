import { createServer } from 'node:http';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { readFile } from 'node:fs/promises';
import { AgentReplay, captureHTTP } from './sdk.ts';
import { shouldCharge } from './decision.mjs';

const exec = promisify(execFile);
const project = (await readFile(new URL('./project.txt', import.meta.url),'utf8')).trim();
const createSDK = commit => new AgentReplay({
  project, service:'agent', commit, policyVersion:'demo-v1', directory:'/tmp/capture',
  content:['input','output','database:subscription','http:help','model:recommendation'],
  now:()=>new Date('2026-09-27T12:00:00Z'),
});

async function agent(sdk, input) {
  if (!Number.isSafeInteger(input.id) || input.id < 1) throw Error('Invalid synthetic subscription.');
  const subscription = await sdk.boundary({kind:'database',name:'subscription',version:'1',input:{id:input.id}},async()=>{
    const {stdout}=await exec('psql',[process.env.DATABASE_URL,'-At','-v','ON_ERROR_STOP=1','-c',`SELECT row_to_json(s) FROM subscriptions s WHERE id = ${input.id}`]);
    return JSON.parse(stdout);
  });
  const help = await captureHTTP(sdk,'help','http://127.0.0.1:3000/help');
  const guidance = await sdk.boundary({kind:'model',name:'recommendation',version:'1',provider:'synthetic',model:'recorded-demo-v1',instructions:'Use the subscription and help-center rule.',tools:[],settings:{temperature:0},input:{subscription,help}},async()=>({rule:'Charge active subscriptions only.'}));
  return {recommendation:shouldCharge(subscription.status) ? 'charge' : 'review',rule:guidance.rule};
}

createServer(async(req,res)=>{
  res.setHeader('content-type','application/json');
  if(req.url==='/health'){res.end('{"ok":true}');return;}
  if(req.url==='/help'){res.end('{"rule":"Charge active subscriptions only."}');return;}
  if(req.method!=='POST'){res.writeHead(404);res.end('{}');return;}
  try {
    const chunks=[];let size=0;
    for await (const chunk of req){size+=chunk.length;if(size>4*1024*1024)throw Error('Request too large.');chunks.push(chunk);}
    const body=JSON.parse(Buffer.concat(chunks));
    if(req.url==='/capture'){
      const sdk=createSDK(body.commit);
      const output=await sdk.run(body.input,()=>agent(sdk,body.input),{runId:'billing-failure',identity:'synthetic',golden:body.golden});
      const incident=JSON.parse(await readFile('/tmp/capture/billing-failure.json','utf8'));
      res.end(JSON.stringify({output,incident}));return;
    }
    if(req.url==='/af-replay' && process.env.AF_REPLAY_ENABLED==='true'){
      const sdk=createSDK(body.commit);
      res.end(JSON.stringify(await sdk.replay(body,input=>agent(sdk,input))));return;
    }
    res.writeHead(404);res.end('{}');
  }catch{res.writeHead(500);res.end('{"error":"The synthetic agent could not run."}');}
}).listen(3000,'0.0.0.0');
