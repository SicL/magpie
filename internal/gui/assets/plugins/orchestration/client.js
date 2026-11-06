// Projects & Tasks: an opt-in coordination view. DSH headless runs are explicit desktop actions.
magpie.plugin("orchestration", (ctx) => {
  let page = null;
  let projects = [];
  let tasks = [];
  let worktrees = [];
  let runs = [];
  let dependencies = [];
  let capabilities = [];
  let showingCapabilities = false;
  let showingZCodeSetup = false;
  let zcodeConfig = null;
  let zcodeLocalModel = null;
  let zcodeLocalReadiness = null;
  let zcodeProbe = null;
  let showingCodeBuddySetup = false;
  let codeBuddyConfig = null;
  let codeBuddyProbe = null;
  let codeBuddyModelCheck = null;
  let zcodeVerified = new Set();
  let editingDeps = "";
  let dispatchReport = null;
  let dshCapability = null;
  let openLog = "";
  let logText = "";
  let selected = "";
  let projectForm = false;
  let taskForm = false;
  let busy = false;
  let editingCheckpoint = "";
  let recovering = "";
  let showingHistory = "";
  let checkpointRows = [];
  let handoffFor = "";
  let handoffPreview = null;
  let handoffRows = [];
  let handoffCI = null;
  let handoffCIRows = [];
  let recoveryRows = [];

  function field(title, placeholder, value) {
    const box = el("label", "orch-field");
    box.append(el("span", "", t(title)));
    const input = el("input", "set-field");
    input.type = "text";
    input.placeholder = placeholder || "";
    input.value = value || "";
    input.setAttribute("aria-label", t(title));
    box.append(input);
    return { box, input };
  }

  function button(title, action, cls) {
    const b = el("button", cls || "text");
    b.type = "button";
    b.textContent = t(title);
    b.onclick = action;
    return b;
  }

  async function refresh(readyToRender) {
    if (!page) return;
    try {
      projects = await api("orchestration/projects");
      if (!projects.some((p) => p.id === selected)) selected = projects[0]?.id || "";
      tasks = selected ? await api("orchestration/tasks?project=" + encodeURIComponent(selected)) : [];
      worktrees = selected ? await api("orchestration/worktrees?project=" + encodeURIComponent(selected)) : [];
      runs = selected ? await api("orchestration/runs?project=" + encodeURIComponent(selected)) : [];
      dependencies = selected ? await api("orchestration/dependencies?project=" + encodeURIComponent(selected)) : [];
      if (!web && dshCapability===null) { try {dshCapability = await api("orchestration/agents/dsh");} catch {dshCapability={available:false};} }
      if (readyToRender) readyToRender();
      render();
    } catch (e) {
      if (readyToRender) readyToRender();
      page.replaceChildren(el("div", "empty-state", e.message));
    }
  }

  async function perform(work) {
    if (busy) return;
    busy = true;
    page?.classList.add("orch-busy");
    try {
      await work();
      projectForm = false;
      taskForm = false;
      await refresh(() => { busy = false; page?.classList.remove("orch-busy"); });
    } catch (e) {
      status(e.message, "err");
      busy = false;
      page?.classList.remove("orch-busy");
      return;
    }
    busy = false;
    page?.classList.remove("orch-busy");
  }

  function projectEditor() {
    const form = el("form", "orch-form");
    const name = field("Project name", "TMS development");
    const repo = field("Git project folder", "D:\\projects\\repository");
    const branch = field("Base branch (optional)", "main");
    const wt = field("Worktree root (optional)", "D:\\agent-worktrees");
    form.append(name.box, repo.box, branch.box, wt.box);
    const actions = el("div", "orch-actions");
    const save = button("Register project", () => {});
    save.type = "submit";
    save.onclick = null;
    form.onsubmit = (e) => {
      e.preventDefault();
      perform(async () => {
        const p = await api("orchestration/projects", {
          name: name.input.value, repoPath: repo.input.value,
          baseBranch: branch.input.value, worktreeRoot: wt.input.value
        });
        selected = p.id;
        status(t("Project registered"), "ok");
      });
    };
    actions.append(save, button("Cancel", () => { projectForm = false; render(); }));
    form.append(actions);
    return form;
  }

  function taskEditor() {
    const form = el("form", "orch-form");
    const title = field("Task title", "Implement a new API");
    const details = field("Description and acceptance criteria", "Describe what must be verified");
    const agent = field("Assigned agent (optional)", "DeepSeek Harness");
    form.append(title.box, details.box, agent.box);
    const actions = el("div", "orch-actions");
    const save = button("Create task", () => {});
    save.type = "submit";
    save.onclick = null;
    form.onsubmit = (e) => {
      e.preventDefault();
      perform(async () => {
        await api("orchestration/tasks", {
          projectId: selected, title: title.input.value, details: details.input.value, agent: agent.input.value
        });
        status(t("Task created"), "ok");
      });
    };
    actions.append(save, button("Cancel", () => { taskForm = false; render(); }));
    form.append(actions);
    return form;
  }


  function checkpointEditor(task, latestRun) {
    const form=el("form","orch-form");
    const title=el("label","orch-field");
    title.append(el("span","",t("Checkpoint summary")));
    const summary=el("textarea","set-field");
    summary.rows=3;summary.maxLength=4000;
    summary.placeholder=t("Describe progress, blockers and next steps");
    summary.setAttribute("aria-label",t("Checkpoint summary"));
    title.append(summary);form.append(title);
    const result=el("label","orch-field");
    result.append(el("span","",t("Checkpoint test result")));
    const select=el("select","set-field");
    select.setAttribute("aria-label",t("Checkpoint test result"));
    for(const [value,text] of [["not_run","Tests not run"],["passed","Tests passed (self-reported)"],["failed","Tests failed (self-reported)"]]){
      const opt=el("option","",t(text));opt.value=value;select.append(opt);
    }
    result.append(select);form.append(result);
    const actions=el("div","orch-actions");
    const save=button("Save checkpoint",()=>{});
    save.type="submit";save.onclick=null;
    form.onsubmit=(e)=>{
      e.preventDefault();
      perform(async()=>{
        await api("orchestration/tasks/"+encodeURIComponent(task.id)+"/checkpoints",{
          revision:task.revision,summary:summary.value,testStatus:select.value,
          runId:latestRun?.id||""
        });
        editingCheckpoint="";
        status(t("Checkpoint saved"),"ok");
      });
    };
    actions.append(save,button("Cancel",()=>{editingCheckpoint="";render();}));
    form.append(actions);
    return form;
  }

  function recoveryEditor(task,run){
    const form=el("form","orch-form");
    form.append(el("p","orch-safety",t("A previous Agent run was interrupted. Inspect its process tree and Git changes first. Recovery does not stop processes.")));
    const check=el("label","orch-confirm");
    const checkbox=el("input");checkbox.type="checkbox";
    check.append(checkbox,el("span","",t("I verified the old Agent and child processes have stopped and reviewed its Worktree.")));
    form.append(check);
    const reason=el("label","orch-field");
    reason.append(el("span","",t("Recovery audit note")));
    const note=el("textarea","set-field");
    note.rows=3;note.maxLength=500;note.minLength=12;
    note.setAttribute("aria-label",t("Recovery audit note"));
    reason.append(note);form.append(reason);
    const actions=el("div","orch-actions");
    const save=button("Reconcile interrupted run",()=>{});
    save.type="submit";save.onclick=null;
    form.onsubmit=e=>{
      e.preventDefault();
      perform(async()=>{
        await api("orchestration/runs/"+encodeURIComponent(run.id)+"/reconcile",{
          revision:task.revision,verified:checkbox.checked,reason:note.value
        });
        recovering="";
        status(t("Run reconciled; task remains blocked"),"ok");
      });
    };
    actions.append(save,button("Cancel",()=>{recovering="";render();}));
    form.append(actions);
    return form;
  }

  async function openHandoffHistory(taskId){
    await perform(async()=>{
      checkpointRows=await api("orchestration/tasks/"+encodeURIComponent(taskId)+"/checkpoints");
      recoveryRows=await api("orchestration/tasks/"+encodeURIComponent(taskId)+"/recoveries");
      handoffRows=await api("orchestration/tasks/"+encodeURIComponent(taskId)+"/handoffs");
      handoffCIRows=await api("orchestration/tasks/"+encodeURIComponent(taskId)+"/handoff/ci-proofs");
      showingHistory=taskId;
    });
  }

  async function copyHandoff(task,project,worktree,latestRun,clicked){
    try{
      const checkpoints=await api("orchestration/tasks/"+encodeURIComponent(task.id)+"/checkpoints");
      const latest=checkpoints[0];
      const snapshot=await api("orchestration/tasks/"+encodeURIComponent(task.id)+"/handoff");
      const lines=[
        "Project: "+project.name,
        "Task: "+task.title,
        "Assigned agent: "+(task.agent||"unassigned"),
        "Repository: "+project.repoPath,
        "Worktree (task-scoped): "+worktree.path,
        "Git branch: "+worktree.branch,
        "Task state: "+task.status,
        "Acceptance criteria: "+task.details,
        "Latest checkpoint: "+(latest?.summary||"none"),
        "Tests: "+(latest?.testStatus||"not_run"),
        "Git HEAD: "+(snapshot?.gitHead||latest?.gitHead||worktree.head||"unknown"),
        "Handoff readiness: "+(snapshot?.ready===true?"verified snapshot": "NOT verified"),
        "Handoff blocker: "+(snapshot?.reason||"none"),
        "Snapshot fingerprint: "+(snapshot?.fingerprint||"unavailable"),
        "Uncommitted changes: "+(latest?.dirty?"yes":"unknown"),
        "Native DSH session: "+(latestRun?.sessionId||"none"),
        "Do not let multiple Agents write this same Worktree concurrently."
      ];
      await copy(lines.join("\n"),t("Task handoff"),clicked,t("Handoff copied"));
    }catch(e){status(e.message,"err");}
  }



  function zcodeLocalModelEditor(){
    const form=el("form","orch-form");
    form.append(el("b","",t("Local model configuration")));
    form.append(el("p","orch-safety",t("Only a model on 127.0.0.1 or [::1] is supported. No API keys are saved or inherited; start your local model server separately.")));
    const address=field("Local model API URL","http://127.0.0.1:1234/v1",zcodeLocalModel?.baseUrl||"http://127.0.0.1:1234/v1");
    const model=field("Local model ID","qwen-local",zcodeLocalModel?.modelId||"");
    form.append(address.box,model.box);
    if(zcodeLocalReadiness){
      const labels={
        unconfigured:t("Local model is not configured"),
        unreachable:t("Local model server cannot be reached"),
        unexpected_response:t("Model server response is not usable"),
        model_missing:t("Configured model was not found on server"),
        ready:t("Local model is listed by the server")
      };
      const line=el("p","orch-capability",
        (labels[zcodeLocalReadiness.status]||t("Model server response is not usable"))+
        " · "+zcodeLocalReadiness.availableModels+" "+t("models available"));
      form.append(line);
    }
    form.append(el("p","orch-safety",t("A model-list check does not authorize Agent execution; task model verification is still required.")));
    const actions=el("div","orch-actions");
    const save=button("Save local model",()=>{});
    save.type="submit";save.onclick=null;
    form.onsubmit=e=>{
      e.preventDefault();
      perform(async()=>{
        zcodeLocalModel=await api("orchestration/agents/zcode/local-model",{baseUrl:address.input.value,modelId:model.input.value});
        zcodeVerified.clear();
        zcodeLocalReadiness=null;
        status(t("Local model saved; reverify task model"),"ok");
      });
    };
    actions.append(save,button("Check local model server",()=>perform(async()=>{
      zcodeLocalReadiness=await api("orchestration/agents/zcode/local-model/check",{confirm:true});
      status(zcodeLocalReadiness.status==="ready"?t("Local model is listed by the server"):t("Local model server check completed"),
             zcodeLocalReadiness.status==="ready"?"ok":"err");
    })));
    form.append(actions);
    return form;
  }

  function zcodeSettingsEditor() {
    const form=el("form","orch-form");
    form.append(el("b","",t("ZCode CLI setup")));
    form.append(el("p","orch-safety",t("The standalone CLI is not ZCode Desktop. Plan mode is not an operating-system sandbox; do not supply production credentials.")));
    const cli=field("ZCode CLI bundle path","D:\\tools\\zcode\\zcode.cjs",zcodeConfig?.executable||"");
    form.append(cli.box);
    if(zcodeProbe) {
      const state=zcodeProbe.cliFound?t("ZCode plan CLI contract verified"):t("ZCode CLI check failed");
      form.append(el("p","orch-capability",state+": "+(zcodeProbe.reason||"")));
    }
    const actions=el("div","orch-actions");
    const save=button("Save ZCode CLI path",()=>{});
    save.type="submit";save.onclick=null;
    form.onsubmit=e=>{
      e.preventDefault();
      perform(async()=>{
        zcodeConfig=await api("orchestration/agents/zcode/config",{executable:cli.input.value});
        zcodeProbe=null;
        zcodeVerified.clear();
        zcodeLocalReadiness=null;
        status(t("ZCode CLI path saved"),"ok");
      });
    };
    actions.append(save,button("Verify ZCode CLI",()=>perform(async()=>{
      zcodeProbe=await api("orchestration/agents/zcode/probe",{confirm:true});
      zcodeVerified.clear();
      if(zcodeProbe.cliFound){
        for(const task of tasks.filter(q=>["zcode","zcode cli","zcode-cli"].includes((q.agent||"").trim().toLowerCase()))){
          try {
            const evidence=await api("orchestration/tasks/"+encodeURIComponent(task.id)+"/zcode/verification");
            if(evidence.verified) zcodeVerified.add(task.id);
          } catch { /* Missing or unverified must never enable launch. */ }
        }
      }
      status(zcodeProbe.reason,zcodeProbe.cliFound?"ok":"err");
    })));
    actions.append(button("Cancel",()=>{showingZCodeSetup=false;render();}));
    form.append(actions);
    return form;
  }

  function codeBuddySettingsEditor(){
    const form=el("form","orch-form");
    form.append(el("b","",t("CodeBuddy CLI setup")));
    form.append(el("p","orch-safety",t("Independent CodeBuddy CLI only; authentication and tool permissions are not verified. Manual handoff only; no automatic execution or -y bypass.")));
    const cli=field("CodeBuddy CLI executable","D:\\tools\\codebuddy\\node_modules\\@tencent-ai\\codebuddy-code\\bin\\codebuddy",codeBuddyConfig?.executable||"");
    form.append(cli.box);
    if(codeBuddyProbe){
      const label=codeBuddyProbe.cliFound?t("CodeBuddy CLI help contract verified"):t("CodeBuddy CLI probe failed");
      form.append(el("p","orch-capability",label+": "+(codeBuddyProbe.reason||"")));
    }
    if(codeBuddyModelCheck){
      const labels={
        unconfigured:t("CodeBuddy CLI is not configured"),
        cli_unavailable:t("CodeBuddy CLI help check failed"),
        unsafe_cli_contract:t("CodeBuddy cannot disable all tools"),
        unverified:t("CodeBuddy isolated model reply not verified"),
        timed_out:t("CodeBuddy isolated model check timed out"),
        model_replied:t("CodeBuddy responded to isolated no-tools prompt")
      };
      form.append(el("p","orch-capability",
        labels[codeBuddyModelCheck.status]||t("CodeBuddy isolated model reply not verified")));
    }
    form.append(el("p","orch-safety",t("This fixed-prompt check uses a temporary empty workspace and stores no credentials. Even success does not authorize code access, automated tasks or file changes.")));
    const actions=el("div","orch-actions");
    const save=button("Save CodeBuddy CLI path",()=>{});
    save.type="submit";save.onclick=null;
    form.onsubmit=e=>{
      e.preventDefault();
      perform(async()=>{
        codeBuddyConfig=await api("orchestration/agents/codebuddy/config",{executable:cli.input.value});
        codeBuddyProbe=null;
        codeBuddyModelCheck=null;
        status(t("CodeBuddy CLI path saved"),"ok");
      });
    };
    actions.append(save,button("Verify CodeBuddy CLI",()=>perform(async()=>{
      codeBuddyProbe=await api("orchestration/agents/codebuddy/probe",{confirm:true});
      status(codeBuddyProbe.reason,codeBuddyProbe.cliFound?"ok":"err");
    })));
    actions.append(button("Check CodeBuddy isolated model",()=>perform(async()=>{
      codeBuddyModelCheck=await api("orchestration/agents/codebuddy/model/check",{confirm:true});
      const passed=codeBuddyModelCheck.status==="model_replied";
      status(passed?t("CodeBuddy responded to isolated no-tools prompt"):t("CodeBuddy isolated model reply not verified"),passed?"ok":"err");
    })));
    actions.append(button("Cancel",()=>{showingCodeBuddySetup=false;render();}));
    form.append(actions);
    return form;
  }
  function dependencyEditor(task) {
    const form=el("form","orch-form");
    form.append(el("strong","",t("Select prerequisite tasks")));
    const available=tasks.filter(x=>x.id!==task.id);
    const checked=new Set(dependencies.filter(d=>d.taskId===task.id).map(d=>d.prerequisiteId));
    const checks=[];
    if(!available.length) form.append(el("p","orch-empty",t("No other tasks in this project")));
    for(const item of available){
      const label=el("label","orch-confirm");
      const input=el("input");
      input.type="checkbox";input.checked=checked.has(item.id);
      label.append(input,el("span","",item.title+" · "+stateLabel(item.status)));
      form.append(label);
      checks.push({id:item.id,input});
    }
    const actions=el("div","orch-actions");
    const save=button("Save prerequisites",()=>{});
    save.type="submit";save.onclick=null;
    form.onsubmit=e=>{
      e.preventDefault();
      perform(async()=>{
        const ids=checks.filter(x=>x.input.checked).map(x=>x.id);
        await api("orchestration/tasks/"+encodeURIComponent(task.id)+"/dependencies",{
          revision:task.revision,prerequisiteIds:ids
        });
        editingDeps="";
        status(t("Prerequisites saved"),"ok");
      });
    };
    actions.append(save,button("Cancel",()=>{editingDeps="";render();}));
    form.append(actions);
    return form;
  }

  function stateLabel(value) {
    const labels = {
      pending: t("Task pending"), ready: t("Task ready"), running: t("Task running"),
      blocked: t("Task blocked"), failed: t("Task failed"),
      review: t("Task review"), done: t("Task complete")
    };
    return labels[value] || value;
  }
  function markLabel(value) {
    const labels = {
      ready: t("Mark ready"), running: t("Mark running"),
      blocked: t("Mark blocked"), failed: t("Mark failed"),
      review: t("Mark review"), done: t("Mark done")
    };
    return labels[value] || value;
  }
  const next = {
    pending: ["ready", "blocked"],
    ready: ["running", "blocked"],
    running: ["review", "failed", "blocked"],
    blocked: ["ready"],
    failed: ["ready"],
    review: ["done", "ready"],
    done: []
  };

  function render() {
    if (!page) return;
    page.replaceChildren();
    const head = el("div", "orch-head");
    const intro = el("div", "orch-intro");
    intro.append(el("b", "", t("Projects and tasks")),
      el("span", "", t(web ? "Browser mode is read-only." : "Assign tasks and start DeepSeek Harness only by explicit action.")));
    head.append(intro);
    if (!web) head.append(button("Add project", () => { projectForm = !projectForm; render(); }, "text"));
    head.append(button("Reload task status", () => refresh(), "text"));

    head.append(button("Agent CLI capabilities",async()=>{
      const opening=!showingCapabilities;
      if(opening){try{capabilities=await api("orchestration/agents/capabilities");}catch(e){status(e.message,"err");return;}}
      showingCapabilities=opening;render();
    },"text"));

    if(!web){
      head.append(button("Configure ZCode CLI",async()=>{
        if(!showingZCodeSetup){
          try{[zcodeConfig,zcodeLocalModel]=await Promise.all([
            api("orchestration/agents/zcode/config"),
            api("orchestration/agents/zcode/local-model")
          ]);}
          catch(e){status(e.message,"err");return;}
        }
        showingZCodeSetup=!showingZCodeSetup;
        render();
      },"text"));
    }

    if(!web){
      head.append(button("Configure CodeBuddy CLI",async()=>{
        if(!showingCodeBuddySetup){
          try{codeBuddyConfig=await api("orchestration/agents/codebuddy/config");}
          catch(e){status(e.message,"err");return;}
        }
        showingCodeBuddySetup=!showingCodeBuddySetup;
        render();
      },"text"));
    }    page.append(head);

    if(showingCapabilities){
      const panel=el("section","orch-capabilities");
      panel.append(el("b","",t("Verified Agent capabilities")));
      for(const entry of capabilities){
        const line=el("div","orch-capability");
        line.append(el("strong","",entry.name));
        line.append(el("span","",entry.autoRunnable?t("Headless execution available"):t("Agent requires manual handoff")));
        if(entry.version) line.append(el("small","",entry.version));
        if(entry.reason) line.append(el("p","",entry.reason));
        panel.append(line);
      }
      page.append(panel);
    }

    if (projectForm) page.append(projectEditor());
    if(showingZCodeSetup && !web) page.append(zcodeSettingsEditor());
    if(showingCodeBuddySetup && !web) page.append(codeBuddySettingsEditor());
    if(showingZCodeSetup && !web) page.append(zcodeLocalModelEditor());

    const grid = el("div", "orch-grid");
    const left = el("section", "orch-section");
    left.append(el("h3", "", t("Projects")));
    if (!projects.length) left.append(el("p", "orch-empty", t("No projects registered")));
    for (const p of projects) {
      const item = button(p.name, async () => {
        selected = p.id;
        taskForm = false;
        await refresh();
      }, "orch-project");
      item.classList.toggle("on", p.id === selected);
      item.title = p.repoPath;
      item.append(el("small", "", p.repoPath));
      left.append(item);
    }
    grid.append(left);

    const right = el("section", "orch-section");
    const toolbar = el("div", "orch-toolbar");
    const active = projects.find((p) => p.id === selected);
    toolbar.append(el("h3", "", active ? active.name : t("Project task list")));
    if (active && !web) toolbar.append(button("Add task", () => { taskForm = !taskForm; render(); }));

    if(active && !web && dshCapability?.available){
      toolbar.append(button("Dispatch ready DSH tasks",()=>perform(async()=>{
        dispatchReport=await api("orchestration/projects/"+encodeURIComponent(active.id)+"/dispatch",{maxRuns:3});
        status(t("DSH dispatch reviewed"),"ok");
      })));
    }

    right.append(toolbar);

      if(active && dispatchReport?.projectId===active.id){
        const reportPanel=el("div","orch-dispatch-report");
        reportPanel.append(el("strong","",t("DSH dispatch summary")));
        reportPanel.append(el("p","",t("DSH runs launched in batch")+": "+dispatchReport.started.length+
          " · "+t("Tasks needing attention")+": "+dispatchReport.skipped.length));
        for(const skip of dispatchReport.skipped){
          const target=tasks.find(x=>x.id===skip.taskId);
          reportPanel.append(el("p","", (target?.title||skip.taskId)+": "+skip.reason));
        }
        right.append(reportPanel);
      }

    if (active) {
      right.append(el("p", "orch-path", active.repoPath + " · " + active.baseBranch));
      if (!web && dshCapability && !dshCapability.available) right.append(el("p", "orch-empty", t("DeepSeek Harness CLI unavailable; tasks remain manual.")));
      if (!web && dshCapability?.available) right.append(el("p","orch-safety",t("Agent runs use your local account permissions; verify each task before starting.")));
      if (taskForm) right.append(taskEditor());
      if (!tasks.length) right.append(el("p", "orch-empty", t("No tasks in this project")));
      for (const q of tasks) {
        const item = el("div", "orch-task");
        const top = el("div", "orch-task-top");
        top.append(el("strong", "", q.title), el("span", "orch-state", t("Workflow state") + ": " + stateLabel(q.status)));
        item.append(top);
        if (q.details) item.append(el("p", "orch-detail", q.details));
        item.append(el("small", "", (q.agent || t("Unassigned")) + " · " + t("Task version") + " " + q.revision));
        const existing = worktrees.find((w) => w.path.endsWith("task-" + q.id.slice(0, 16)));
        const latestRun = runs.find((r) => r.taskId === q.id);

        const required=dependencies.filter(d=>d.taskId===q.id);
        if(required.length){
          const names=required.map(d=>tasks.find(x=>x.id===d.prerequisiteId)?.title||d.prerequisiteId);
          item.append(el("small","orch-deps",t("Task prerequisites")+": "+names.join(" · ")));
        }

        if (existing) {
          item.append(el("small", "orch-worktree", existing.path + " · " + existing.branch));
        }
        if (latestRun) {
          const summary = el("div", "orch-run-summary");
          summary.append(el("span", "", t("Latest agent run") + ": " + latestRun.state));
          summary.append(el("span","",t("Agent adapter")+": "+latestRun.adapter));
          if(latestRun.sessionId) summary.append(el("span","",t("DSH session ID")+": "+latestRun.sessionId));
          if (latestRun.exitCode != null) summary.append(el("span", "", " · " + t("Agent exit code") + " " + latestRun.exitCode));
          if (latestRun.failure) summary.append(el("div", "orch-failure", latestRun.failure));
          item.append(summary);
          if (openLog === latestRun.id && !web) item.append(el("pre", "orch-log", logText));
        }
        const actions = el("div", "orch-actions");
        if (web) { right.append(item); continue; }
        const isDSH = ["deepseek harness", "dsh"].includes((q.agent || "").trim().toLowerCase());
        if (isDSH && existing && q.status === "ready" && dshCapability?.available) {
          actions.append(button("Start DeepSeek Harness", () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/run", {revision:q.revision});
            status(t("Agent process started"), "ok");
          })));
        }
        const assigned=(q.agent||"").trim().toLowerCase();
        if(["zcode","zcode cli","zcode-cli"].includes(assigned) && existing && q.status==="ready" && zcodeProbe?.cliFound){
  actions.append(button("Verify ZCode task model",()=>perform(async()=>{
    const check=await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/zcode/verify",{revision:q.revision,confirm:true});
    if(check.verified){zcodeVerified.add(q.id);status(t("ZCode task model verified"),"ok");}
  })));
}
        if(["zcode","zcode cli","zcode-cli"].includes(assigned) && existing && q.status==="ready" && zcodeProbe?.cliFound && zcodeVerified.has(q.id)){
          actions.append(button("Run ZCode analysis plan",()=>perform(async()=>{
            await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/plan-zcode",{revision:q.revision});
            status(t("ZCode plan process started"),"ok");
          })));
        }

        if (latestRun) {
          actions.append(button("View agent log", () => perform(async () => {
            const log=await api("orchestration/runs/" + encodeURIComponent(latestRun.id) + "/log");
            openLog=latestRun.id;
            logText=log.tail || t("Agent log has no output yet");
          })));
          if (["starting", "running"].includes(latestRun.state)) {
            actions.append(button("Stop owned agent run", () => perform(async () => {
              await api("orchestration/runs/" + encodeURIComponent(latestRun.id) + "/stop", {});
              status(t("Agent stop requested"), "ok");
            })));
          }
        }
        for (const statusName of next[q.status] || []) {
          if (isDSH && existing && statusName === "running") continue;
          actions.append(button(markLabel(statusName), () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/status", {
              revision: q.revision, status: statusName
            });
          })));
        }
        if (!existing && q.status !== "done") {
          actions.append(button("Create isolated Worktree", () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/worktree", {});
            status(t("Worktree created"), "ok");
          })));
        }


        if (existing && ["pending","ready","blocked","failed"].includes(q.status)){
          actions.append(button("Edit prerequisites",()=>{editingDeps=q.id;render();}));
        }
        if(q.status==="ready" && existing){
          actions.append(button("Check dispatch readiness",async()=>{
            try {
              const result=await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/readiness");
              status(result.ready?t("DSH task is ready for dispatch"):result.reason,result.ready?"ok":"err");
            } catch(e) { status(e.message,"err"); }
          }));
        }

        if(isDSH && existing && q.status==="ready" && dshCapability?.available &&
           latestRun?.sessionId && ["completed","failed","cancelled"].includes(latestRun.state)){
          actions.append(button("Resume DSH session",()=>perform(async()=>{
            await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/resume",{
              revision:q.revision,sourceRunId:latestRun.id
            });
            status(t("Native DSH session resumed"),"ok");
          })));
        }
        if(existing) {
          actions.append(button("Add checkpoint",()=>{editingCheckpoint=q.id;render();}));
          actions.append(button("View handoff history",()=>openHandoffHistory(q.id)));
          actions.append(button("Review Git handoff",()=>perform(async()=>{
            handoffPreview=await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/handoff");
            handoffFor=q.id;
            handoffCI=null;
          })));
          const handoffButton=button("Copy task handoff",()=>copyHandoff(q,active,existing,latestRun,handoffButton));
          actions.append(handoffButton);
        }
        if(latestRun?.state==="interrupted" && q.status==="running"){
          actions.append(button("Review interrupted run",()=>{recovering=q.id;render();}));
        }

        if (["pending", "blocked", "failed", "done"].includes(q.status)) {
          actions.append(button("Remove task", () => perform(async () => {
            await api("orchestration/tasks/" + encodeURIComponent(q.id) + "/remove", {});
          }), "text"));
        }
        item.append(actions);
        if(handoffFor===q.id && handoffPreview){
          const panel=el("div","orch-history");
          panel.append(el("b","",t("Verified Git handoff preview")));
          panel.append(el("p","",t("Handoff current Git HEAD")+": "+(handoffPreview.gitHead||t("Handoff HEAD unavailable"))));
          panel.append(el("p","",handoffPreview.clean?t("Handoff Worktree clean"):t("Handoff Worktree has uncommitted changes")));
          panel.append(el("p","",t("Handoff assigned agent")+": "+handoffPreview.agent));
          for(const prior of handoffPreview.prerequisites||[]){
            panel.append(el("p","",prior.agent+": "+prior.testedHead.slice(0,12)+" · "+t("Handoff reported test status")+": "+prior.testsReported));
          }
          if(!handoffPreview.ready){
            panel.append(el("p","orch-failure",t("Handoff blocked")+": "+(handoffPreview.reason||"")));
          }else{
            panel.append(el("p","orch-safety",t("Human acknowledgment records this Git snapshot; it does not certify independent tests or authorize an Agent run.")));
            panel.append(button("Check GitHub CI",()=>perform(async()=>{
              handoffCI=await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/handoff/ci-check",{confirm:true});
              status(handoffCI.status==="verified"?t("GitHub checks verified"):t("GitHub checks not verified"),
                     handoffCI.status==="verified"?"ok":"err");
            })));
            if(handoffCI?.taskId===q.id){
              const statuses={
                verified:t("GitHub checks verified"),
                no_checks:t("No GitHub checks found for this commit"),
                pending:t("GitHub checks are still pending"),
                failed:t("GitHub checks failed"),
                unavailable:t("GitHub checks could not be verified"),
                unsupported_remote:t("Only public github.com remotes are supported"),
                handoff_blocked:t("Git handoff is not ready")
              };
              panel.append(el("p","orch-capability",
                (statuses[handoffCI.status]||t("GitHub checks could not be verified"))+
                " · "+handoffCI.checks+" "+t("independent checks")));
              if(handoffCI.status==="verified"&&handoffCI.fingerprint===handoffPreview.fingerprint){
                panel.append(el("p","orch-safety",t("First acknowledge the Git handoff; then record independently verified CI for the same snapshot. CI evidence does not authorize Agent execution.")));
                panel.append(button("Record verified CI evidence",()=>perform(async()=>{
                  await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/handoff/ci-proofs",{
                    confirm:true,revision:handoffPreview.revision,fingerprint:handoffPreview.fingerprint
                  });
                  handoffCI=null;
                  status(t("GitHub CI evidence recorded"),"ok");
                })));
              }
            }
            panel.append(button("Acknowledge Git handoff",()=>perform(async()=>{
              await api("orchestration/tasks/"+encodeURIComponent(q.id)+"/handoffs",{
                confirm:true,revision:handoffPreview.revision,fingerprint:handoffPreview.fingerprint
              });
              handoffPreview=null;
              handoffFor="";
              status(t("Git handoff receipt recorded"),"ok");
            })));
          }
          panel.append(button("Cancel",()=>{handoffFor="";handoffPreview=null;render();}));
          item.append(panel);
        }
        if (editingCheckpoint===q.id) item.append(checkpointEditor(q,latestRun));
        if (editingDeps===q.id) item.append(dependencyEditor(q));
        if (recovering===q.id && latestRun?.state==="interrupted") item.append(recoveryEditor(q,latestRun));
        if (showingHistory===q.id){
          const history=el("div","orch-history");
          history.append(el("b","",t("Task handoff checkpoints")));
          if(!checkpointRows.length) history.append(el("p","orch-empty",t("No checkpoints yet")));
          for(const c of checkpointRows){
            const entry=el("div","orch-history-entry");
            entry.append(el("span","",c.createdAt+" · "+c.testStatus+" · "+c.gitHead.slice(0,10)+(c.dirty?" · "+t("Uncommitted Worktree changes"):"")));
            entry.append(el("p","",c.summary));
            history.append(entry);
          }
          for(const receipt of handoffRows){
            const entry=el("div","orch-history-entry");
            entry.append(el("span","",t("Acknowledged Git handoff")+" · "+receipt.recipient+" · "+receipt.createdAt));
            entry.append(el("p","",receipt.gitHead.slice(0,12)+" · "+receipt.fingerprint.slice(0,12)));
            history.append(entry);
          }
          for(const proof of handoffCIRows){
            const entry=el("div","orch-history-entry");
            entry.append(el("span","",t("Independent GitHub CI verified")+" · "+proof.repository+" · "+proof.checkedAt));
            entry.append(el("p","",proof.gitHead.slice(0,12)+" · "+proof.checks+" "+t("independent checks")));
            history.append(entry);
          }
          for(const r of recoveryRows){
            const entry=el("div","orch-history-entry");
            entry.append(el("span","",t("Manual recovery")+" · "+r.createdAt));
            entry.append(el("p","",r.reason));
            history.append(entry);
          }
          item.append(history);
        }
        right.append(item);
      }
      if (!tasks.length && !web) {
        right.append(button("Remove empty project", () => perform(async () => {
          await api("orchestration/projects/" + encodeURIComponent(active.id) + "/remove", {});
          selected = "";
        })));
      }
    } else {
      right.append(el("p", "orch-empty", t("Register a Git repository to start")));
    }
    grid.append(right);
    page.append(grid);
  }

  ctx.slots.add("page", {
    key: "orchestration", title: "Projects",
    mount(main) { page = main; },
    open: () => refresh(),
    relang: () => render(),
  });
});
