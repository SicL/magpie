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

  if(url.pathname==="/api/copy"){data.copied=req.postDataJSON().text;return json({ok:true});}
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/checkpoints"){
   if(req.method()==="POST"){
    const v=req.postDataJSON();
    const c={id:"cp1",taskId:data.tasks[0].id,summary:v.summary,testStatus:v.testStatus,gitHead:"f".repeat(40),dirty:false,createdAt:"2026-11-06T11:00:00Z"};
    data.checkpoints.unshift(c);
    return json(c,201);
   }
   return json(data.checkpoints);
  }
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/recoveries") return json(data.recoveries);
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/resume"){
   const v=req.postDataJSON();
   const run={id:"00112233aabbccddeeff001122334455",taskId:data.tasks[0].id,resumeOf:v.sourceRunId,
    adapter:"deepseek-harness",state:"running",pid:234,worktreePath:data.worktrees[0].path,sessionId:"session-fixture-001"};
   data.runs.unshift(run);data.tasks[0].status="running";data.tasks[0].revision++;
   return json(run,201);
  }
  if(url.pathname==="/api/orchestration/runs/00112233aabbccddeeff001122334455/reconcile"){
   const v=req.postDataJSON();
   const rec={id:"recovery-1",runId:data.runs[0].id,taskId:data.tasks[0].id,reason:v.reason,
     createdAt:"2026-11-06T11:05:00Z",gitHead:"f".repeat(40),dirty:false};
   data.recoveries.unshift(rec);
   data.runs[0].state="reconciled";
   data.tasks[0].status="blocked";
   data.tasks[0].revision++;
   return json(rec);
  }


  if(url.pathname==="/api/orchestration/agents/capabilities") return json([
    {id:"deepseek-harness",name:"DeepSeek Harness",cliFound:true,autoRunnable:true,version:"test",reason:"verified fixture"},
    {id:"workbuddy-desktop",name:"WorkBuddy Desktop",cliFound:false,autoRunnable:false,reason:"manual handoff"},
    {id:"zcode-desktop",name:"ZCode Desktop",cliFound:false,autoRunnable:false,reason:"manual handoff"},
    {id:"codebuddy-code-cli",name:"CodeBuddy Code CLI",cliFound:false,autoRunnable:false,reason:"not installed"},
    {id:"zcode-cli",name:"ZCode Agent CLI",cliFound:false,autoRunnable:false,reason:"not installed"}
  ]);
  if(url.pathname==="/api/orchestration/dependencies") return json(data.dependencies);
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/dependencies") {
    const v=req.postDataJSON();
    data.dependencies=v.prerequisiteIds.map(prerequisiteId=>({taskId:data.tasks[0].id,prerequisiteId}));
    data.tasks[0].revision++;
    return json(data.tasks[0]);
  }
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/readiness")
    return json({taskId:data.tasks[0].id,ready:data.tasks[0].status==="ready",prerequisites:0});
  if(url.pathname==="/api/orchestration/projects/p1/dispatch")
    return json({projectId:"p1",started:[],skipped:[]});

  if(url.pathname==="/api/orchestration/agents/zcode/config"){
    if(req.method()==="POST"){
      data.zcodeConfig={executable:req.postDataJSON().executable,configured:true,mode:"plan"};
    }
    return json(data.zcodeConfig);
  }
  if(url.pathname==="/api/orchestration/agents/zcode/probe")
    return json({id:"zcode-cli-plan",name:"ZCode CLI (plan mode)",cliFound:true,autoRunnable:true,version:"0.17.1-fixture",reason:"isolated CLI fixture verified"});
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/zcode/verification"){
    return json({taskId:data.tasks[0].id,verified:data.zcodeVerified});
  }
  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/zcode/verify"){
    data.zcodeVerified=true;
    return json({taskId:data.tasks[0].id,verified:true,checkedAt:"2026-11-06T11:10:00Z"});
  }

  if(url.pathname==="/api/orchestration/tasks/00112233445566778899aabbccddeeff/plan-zcode"){
    const run={id:"zcode-test-run",taskId:data.tasks[0].id,adapter:"zcode-cli-plan",state:"running",worktreePath:data.worktrees[0].path};
    data.runs.unshift(run);data.tasks[0].status="running";data.tasks[0].revision++;
    return json(run,201);
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
  const data={projects:[],tasks:[],worktrees:[],runs:[],checkpoints:[],recoveries:[],dependencies:[],copied:"",zcodeConfig:{configured:false,mode:"plan",executable:""},zcodeVerified:false};
  await page.route("**/*",serve("zh",data));
  await page.goto("http://magpie.test/?view=orchestration");
  await page.locator("#view-orchestration .orch-head").waitFor({timeout:10000});
  assert.match(await page.locator("#view-orchestration .orch-intro").innerText(),/项目与任务/);
  await page.locator("#view-orchestration").getByRole("button",{name:"Agent CLI 能力"}).click();
  assert.match(await page.locator("#view-orchestration .orch-capabilities").innerText(),/ZCode Desktop/);
  assert.match(await page.locator("#view-orchestration .orch-capabilities").innerText(),/仅支持人工交接/);
  await page.locator("#view-orchestration").getByRole("button",{name:"Agent CLI 能力"}).click();
  await page.locator("#view-orchestration").getByRole("button",{name:"配置 ZCode CLI"}).click();
  await page.locator("#view-orchestration .orch-form").getByRole("textbox",{name:"ZCode CLI 文件路径"}).fill("C:\\tools\\zcode\\zcode.cjs");
  await page.locator("#view-orchestration .orch-form").getByRole("button",{name:"保存 ZCode CLI 路径"}).click();
  await page.waitForFunction(() => !document.querySelector("#view-orchestration")?.classList.contains("orch-busy"));
  assert.equal(data.zcodeConfig.configured,true);
  await page.locator("#view-orchestration .orch-form").getByRole("button",{name:"核验 ZCode CLI"}).click();
  await page.locator("#view-orchestration").getByText(/ZCode CLI 计划模式参数核验通过/).waitFor();
  await page.locator("#view-orchestration").getByRole("button",{name:"配置 ZCode CLI"}).click();

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
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"编辑前置任务"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"保存前置任务"}).click();
  await page.locator("#view-orchestration .orch-task" ).getByRole("button",{name:"保存前置任务"}).waitFor({state:"detached"});
  assert.equal(data.dependencies.length,0);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"新增检查点"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("textbox",{name:"检查点摘要"}).fill("Reviewed interface and tests");
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"保存检查点"}).click();
  await page.locator("#view-orchestration .orch-task" ).getByRole("button",{name:"保存检查点"}).waitFor({state:"detached"});
  assert.equal(data.checkpoints.length,1);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"查看交接历史"}).click();
  await page.waitForFunction(() => document.querySelector("#view-orchestration .orch-history")?.textContent?.includes("Reviewed interface and tests"));
  await Promise.all([
    page.waitForResponse(r=>r.url().endsWith("/api/copy")),
    page.locator("#view-orchestration .orch-task").getByRole("button",{name:"复制任务交接信息"}).click()
  ]);
  assert.match(data.copied,/Worktree \(task-scoped\)/);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"标记就绪"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"检查调度条件"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"启动 DeepSeek Harness"}).click();
  await page.locator("#view-orchestration .orch-run-summary").waitFor();
  assert.equal(data.runs.length,1);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"查看 Agent 日志"}).click();
  assert.match(await page.locator("#view-orchestration .orch-log").innerText(),/fixture safe log line/);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"停止此 Agent 运行"}).click();
  assert.equal(data.runs[0].state,"stopping");
  data.runs[0].state="completed";data.runs[0].sessionId="session-fixture-001";
  data.tasks[0].status="review";data.tasks[0].revision++;
  await page.locator("#view-orchestration .orch-head").getByRole("button",{name:"刷新任务状态"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"标记就绪"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"检查调度条件"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"续接 DSH 会话"}).click();
  assert.equal(data.runs.length,2);
  assert.equal(data.runs[0].resumeOf,data.runs[1].id);
  data.runs[0].state="interrupted";
  await page.locator("#view-orchestration .orch-head").getByRole("button",{name:"刷新任务状态"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"核验中断运行"}).click();
  await page.locator("#view-orchestration .orch-confirm input").check();
  await page.locator("#view-orchestration .orch-task").getByRole("textbox",{name:"恢复核验备注"}).fill("Confirmed old process tree stopped and Git worktree reviewed");
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"核销中断运行"}).click();
  assert.equal(data.recoveries.length,1);
  assert.equal(data.tasks[0].status,"blocked");
  await page.waitForFunction(() => !document.querySelector("#view-orchestration")?.classList.contains("orch-busy"));
  data.tasks[0].agent="ZCode";data.tasks[0].status="ready";data.tasks[0].revision++;
  data.runs[0].state="completed";
  await page.locator("#view-orchestration .orch-head").getByRole("button",{name:"刷新任务状态"}).click();
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"核验 ZCode 任务模型"}).click();
  await page.waitForFunction(() => !document.querySelector("#view-orchestration")?.classList.contains("orch-busy"));
  assert.equal(data.zcodeVerified,true);
  await page.locator("#view-orchestration .orch-task").getByRole("button",{name:"启动 ZCode 分析计划"}).click();
  await page.waitForFunction(() => document.querySelector("#view-orchestration .orch-run-summary")?.textContent?.includes("zcode-cli-plan") || document.querySelector("#view-orchestration .orch-task")?.textContent?.includes("zcode-cli-plan"));
  assert.equal(data.runs[0].adapter,"zcode-cli-plan");

  await page.setViewportSize({width:420,height:740});
  await page.locator("#view-orchestration").getByRole("button",{name:"派发就绪 DSH 任务"}).click();
  assert.match(await page.locator("#view-orchestration .orch-dispatch-report").innerText(),/DSH 派发结果/);
  const sizing=await page.locator("#view-orchestration .orch-grid").evaluate((node)=>({width:node.clientWidth,scroll:node.scrollWidth}));
  assert.ok(sizing.scroll<=sizing.width+2, "narrow layout overflow: "+JSON.stringify(sizing));
 });
}
