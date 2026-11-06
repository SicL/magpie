// Projects & Tasks: the actual Magpie page with isolated HTTP fixtures.
// No live Magpie configuration, agent or Git repository is touched.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const {test} = require("node:test");
const {chromium,webkit} = require("playwright");

const assets = path.resolve(__dirname,"../assets");

function serve(lang, data) {
 return async (route) => {
  const req=route.request(), url=new URL(req.url());
  const json=(value,code=200)=>route.fulfill({status:code,json:value});
  if(url.pathname==="/boot.js") return route.fulfill({contentType:"text/javascript",body:"window.bootPrefs = "+JSON.stringify({lang,theme:"light",web:false})+";"});
  if(url.pathname==="/wails/runtime.js") return route.fulfill({contentType:"text/javascript",body:"export const Window = {};"});
  if(url.pathname==="/api/state") return json({agents:[],profiles:[],settings:{lang,theme:"light",builtins:[{id:"orchestration",on:true}]}});
  if(url.pathname==="/api/providers") return json({providers:[],presets:[],gateway:{running:true,window:true,mine:true},plugins:[]});
  if(url.pathname==="/api/groups") return json({groups:[]});
  if(url.pathname==="/api/orchestration/projects") {
   if(req.method()==="POST"){
    const v=req.postDataJSON();
    data.projects.push({id:"p1",name:v.name,repoPath:v.repoPath,baseBranch:"main",worktreeRoot:"C:\\tmp\\worktrees"});
    return json(data.projects[0],201);
   }
   return json(data.projects);
  }
  if(url.pathname==="/api/orchestration/tasks"){
   if(req.method()==="POST"){
    const v=req.postDataJSON();
    data.tasks.push({id:"00112233445566778899aabbccddeeff",projectId:v.projectId,title:v.title,details:v.details,agent:v.agent,status:"pending",revision:1});
    return json(data.tasks[0],201);
   }
   return json(data.tasks);
  }
  if(url.pathname==="/api/orchestration/agents/dsh") return json({adapter:"deepseek-harness",available:true,version:"fixture"});
  if(url.pathname==="/api/orchestration/runs") return json(data.runs);
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/status") {
   const input=req.postDataJSON();data.tasks[0].status=input.status;data.tasks[0].revision++;
   return json(data.tasks[0]);
  }
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/run") {
   const run={id:"aabbccddeeff00112233445566778899",taskId:data.tasks[0].id,adapter:"deepseek-harness",state:"running",pid:123,worktreePath:data.worktrees[0].path};
   data.runs.unshift(run);data.tasks[0].status="running";data.tasks[0].revision++;
   return json(run,201);
  }
  if(url.pathname==="/api/orchestration/runs/aabbccddeeff00112233445566778899/log") return json({tail:"fixture safe log line"});
  if(url.pathname==="/api/orchestration/runs/aabbccddeeff00112233445566778899/stop") {
   data.runs[0].state="stopping";return json({stopping:true});
  }
  if(url.pathname==="/api/orchestration/worktrees") return json(data.worktrees);
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/worktree"){
   data.worktrees.push({taskId:data.tasks[0].id,path:"C:\\tmp\\worktrees\\task-0011223344556677",branch:"agent/task-0011223344556677",head:"f".repeat(40)});
   return json(data.worktrees[0],201);
  }
  if(url.pathname.startsWith("/api/")) return json({});
  if(url.host!=="magpie.test") return route.fulfill({status:404,body:""});
  const file=path.join(assets,url.pathname==="/"?"index.html":url.pathname);
  const type={".html":"text/html",".js":"text/javascript",".css":"text/css",".svg":"image/svg+xml",".png":"image/png"}[path.extname(file)];
  try{return route.fulfill({body:await fs.readFile(file),contentType:type});}
  catch {return route.fulfill({status:404,body:""});}
 };
}

for(const engine of ["chromium","webkit"]){
 test(engine+": project and task can be registered and isolated worktree created",async (t)=>{
  const browser=await (engine==="webkit"?webkit.launch():chromium.launch({channel:"chromium"}));
  t.after(()=>browser.close());
  const ctx=await browser.newContext({viewport:{width:1150,height:760},reducedMotion:"reduce"});
  t.after(()=>ctx.close());
  const page=await ctx.newPage();
  page.setDefaultTimeout(9000);
  const data={projects:[],tasks:[],worktrees:[],runs:[]};
  await page.route("**/*",serve("zh",data));
  await page.goto("http://magpie.test/?view=orchestration");
  await page.locator("#view-orchestration .orch-head").waitFor({timeout:10000});
  assert.match(await page.locator("#view-orchestration .orch-intro").innerText(),/项目与任务/);
  await page.locator("#view-orchestration").getByRole("button",{name:"添加项目"}).click();
  const projectForm=page.locator("#view-orchestration .orch-form");
  await projectForm.getByRole("textbox",{name:"项目名称"}).fill("TMS Test");
  await projectForm.getByRole("textbox",{name:"Git 仓库根目录"}).fill("C:\\temp\\tms-test");
  await projectForm.getByRole("button",{name:"登记项目"}).click();
  await page.locator("#view-orchestration .orch-project").filter({hasText:"TMS Test"}).waitFor();
  await page.locator("#view-orchestration").getByRole("button",{name:"添加任务"}).click();
  const taskForm=page.locator("#view-orchestration .orch-form");
  await taskForm.getByRole("textbox",{name:"任务名称"}).fill("Develop API");
  await taskForm.getByRole("textbox",{name:"分配 Agent（可选）"}).fill("DeepSeek Harness");
  await taskForm.getByRole("button",{name:"创建任务"}).click();
  await page.locator("#view-orchestration .orch-task").filter({hasText:"Develop API"}).waitFor();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"创建隔离 Worktree"}).click();
  await page.locator("#view-orchestration .orch-worktree").waitFor();
  assert.equal(data.projects.length,1);
  assert.equal(data.tasks.length,1);
  assert.equal(data.worktrees.length,1);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"标记就绪"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"启动 DeepSeek Harness"}).click();
  await page.locator("#view-orchestration .orch-run-summary").waitFor();
  assert.equal(data.runs.length,1);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"查看 Agent 日志"}).click();
  assert.match(await page.locator("#view-orchestration .orch-log").innerText(),/fixture safe log line/);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"停止此 Agent 运行"}).click();
  assert.equal(data.runs[0].state,"stopping");
  await page.setViewportSize({width:420,height:740});
  const sizing=await page.locator("#view-orchestration .orch-grid").evaluate((node)=>({width:node.clientWidth,scroll:node.scrollWidth}));
  assert.ok(sizing.scroll<=sizing.width+2, "narrow layout overflow: "+JSON.stringify(sizing));
 });
}
