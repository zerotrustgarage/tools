(() => {
  const calls = window.__calls = [];
  const copy = value => JSON.parse(JSON.stringify(value));
  const base = {schema:1,source:'',destination:'',options:['/E','/MT:8','/DCOPY:DAT','/R:2','/W:5','/XJ']};
  const templates = [{schema:1,catalog_version:1,id:'sample',description:'<img src=x onerror="window.__pwned=true">',fields:[{name:'source',kind:'path',description:'Source folder'},{name:'destination',kind:'path',description:'Destination folder'}],job:{...base,source:'{source}',destination:'{destination}'}}];
  let state={id:0,running:false,messages:[],phase:''}, sequence=0, ticks=0, active, approved='';
  const summary={directories:{total:2,copied:0,skipped:2,mismatch:0,failed:0,extras:0},files:{total:12,copied:12,skipped:0,mismatch:0,failed:0,extras:0},bytes:{total:8388608,copied:8388608,skipped:0,mismatch:0,failed:0,extras:0}};
  const diagnostics={errors:[{code:32,count:2,cause:'File in use',suggestion:'Close the application holding the file, or retry during a maintenance window.',samples:['2026/10/23 12:34:56 ERROR 32 (0x00000020) Copying File C:\\日本語\\<img src=x onerror="window.__pwned=true">.txt']}]};
  const known = new Map(window.__catalog.map(x=>[x.token,x]));
  const describe = j => {
    const issues=[];
    for(const a of j.options || []) if(a.startsWith('/') && !known.has(a.split(':')[0].toUpperCase())) issues.push({severity:'error',argument:a,message:'not recognized by this catalog; retained for review'});
    let restriction='';
    if(!j.source || !j.destination) restriction='Choose both folders';
    if((j.options||[]).some(a=>['/MIR','/PURGE','/MOVE','/REG'].includes(a.split(':')[0]))) restriction='This option is not enabled in the initial copy runner';
    if(issues.length) restriction=issues[0].message;
    return {job:copy(j),command:"& (Join-Path ([Environment]::SystemDirectory) 'Robocopy.exe') "+[j.source,j.destination,...(j.options||[])].map(a=>"'"+String(a).replaceAll("'","''")+"'").join(' '),issues,can_run:!restriction,restriction};
  };
  const history=[{id:'2026-10-22/0123456789abcdef01234567',started:'2026-10-22T12:00:00Z',state:'copied',mode:'run',source:'<img src=x onerror="window.__pwned=true">',destination:'D:\\Archive',exit:0}];
  window.runtime={
    WindowSetTitle:title=>calls.push(['WindowSetTitle',title]),WindowSetBackgroundColour:()=>{},
    WindowSetDarkTheme:()=>{},WindowSetLightTheme:()=>{},WindowSetSystemDefaultTheme:()=>{},
    WindowMinimise:()=>calls.push(['Minimize']),WindowToggleMaximise:()=>calls.push(['Maximize']),
    WindowIsMaximised:()=>false,Quit:()=>calls.push(['Quit']),ClipboardSetText:text=>calls.push(['Clipboard',text]),
    BrowserOpenURL:url=>calls.push(['BrowserOpenURL',url])
  };
  window.go={main:{App:{
    Bootstrap:async()=>({version:'0.1.0-preview',catalog:window.__catalog,records:'C:\\Users\\Technician\\AppData\\Local\\ZTG\\Copy\\runs',templates:'C:\\Users\\Technician\\AppData\\Roaming\\ZTG\\Copy\\templates',default_job:base}),
    BitsCatalog:async()=>window.__bitsCatalog,
    DefaultBitsJob:async()=>({schema:1,engine:'bits',transfers:[],options:[{name:'TransferType',values:['Download']},{name:'Priority',values:['Foreground']}]}),
    DescribeBits:async j=>{
      const issues=[];
      if(!j.transfers.length||j.transfers.some(file=>!file.source||!file.destination))issues.push({severity:'error',argument:'files',message:'Provide matching source and destination files'});
      for(const option of j.options){
        if(!window.__bitsCatalog.find(entry=>entry.name.toLowerCase()===option.name.toLowerCase()&&entry.available))issues.push({severity:'error',argument:option.name,message:'Unknown or reference-only parameter; retained for review'});
        if(option.name==='RetryInterval'&&Number(option.values[0])<60)issues.push({severity:'error',argument:'RetryInterval',message:'The minimum is 60 seconds'});
      }
      const quote=value=>"'"+String(value).replaceAll("'","''")+"'";
      const command='BitsTransfer\\Start-BitsTransfer -Source '+j.transfers.map(file=>quote(file.source)).join(', ')+' -Destination '+j.transfers.map(file=>quote(file.destination)).join(', ')+' '+j.options.map(option=>'-'+option.name+(option.name==='Credential'||option.name==='ProxyCredential'?' (Get-Credential)':option.values?.length?' '+option.values.map(quote).join(', '):'')).join(' ')+' -ErrorAction Stop';
      return {job:copy(j),valid:issues.length===0,issues,command:issues.length?'':command};
    },
    PickBitsFile:async kind=>kind==='source'?'C:\\Local\\source.bin':'C:\\Downloads\\file.bin',
    ImportBitsJob:async()=>{calls.push(['ImportBitsJob']);return {job:window.__bitsImport||{schema:1,engine:'bits',transfers:[{source:'https://example.net/file.bin',destination:'C:\\Downloads\\file.bin'}],options:[]}};},
    ExportBitsJob:async j=>{calls.push(['ExportBitsJob',copy(j)]);return 'C:\\bits-transfer.json';},
    ExportBitsScript:async j=>{calls.push(['ExportBitsScript',copy(j)]);return 'C:\\bits-transfer.ps1';},
    Describe:async j=>describe(j),
    PickDirectory:async kind=>kind==='source'?'C:\\Company Data':'D:\\Archive',
    ChooseRecordsDirectory:async()=>{calls.push(['ChooseRecordsDirectory']);return 'D:\\Copy records';},
    Explain:async text=>{calls.push(['Explain',text]);return describe({schema:1,source:'C:\\Data',destination:'D:\\Archive',options:text.includes('/FUTURE')?['/E','/FUTURE:9']:['/E']});},
    ImportJob:async()=>describe({...base,source:'C:\\Imported',destination:'D:\\Imported'}),
    ExportJob:async j=>{calls.push(['ExportJob',j]);return 'D:\\copy-job.json';},
    ImportRCJ:async()=>{
      calls.push(['ImportRCJ']);
      if(window.__rcjCancel)return null;
      const j=copy(window.__rcjJob),options=j.options||[],switches=[];
      for(let i=0;i<options.length;i++){
        const a=options[i],o=known.get(a.split(':')[0].toUpperCase()),s={argument:a,known:Boolean(o&&a.startsWith('/')),hidden:Boolean(o?.tags?.includes('hidden')),destructive:Boolean(o?.tags?.includes('destructive')),available:Boolean(o?.available),help:o?.help||'',handling:o?.handling||'',tags:o?.tags||[]};
        if(o?.argument==='list')for(s.items=[];i+1<options.length&&!options[i+1].startsWith('/');)s.items.push(options[++i]);
        switches.push(s);
      }
      const view=describe(j);
      const notes=switches.some(s=>s.destructive)?[{severity:'warning',argument:'/MIR',message:'destructive: a list-only preview and a confirmation are required before it can run'}]:[];
      return {file:window.__rcjFile,encoding:'UTF-8',bytes:321,view,notes,switches,needs_review:view.issues.length>0||switches.some(s=>!s.known||s.hidden||s.destructive)};
    },
    PreviewRCJExport:async(j,mode)=>{
      calls.push(['PreviewRCJExport',copy(j),mode]);
      const bad=describe(j).issues.find(i=>i.severity==='error');
      if(!j.source||!j.destination)throw Error('job requires review before export: '+(j.source?'destination':'source')+': use an absolute drive or UNC path without device prefixes or wildcards');
      if(bad)throw Error('job requires review before export: '+bad.argument+': '+bad.message);
      const text='::\r\n:: Robocopy job file written by ZTG Copy. It holds no credentials.\r\n\t/SD:'+j.source+'\r\n\t/DD:'+j.destination+'\r\n';
      return {text,encoding:mode==='utf16'?'UTF-16LE with byte order mark':mode==='utf8'?'UTF-8 without byte order mark':'ASCII (plain bytes, the same in ANSI and UTF-8)',bytes:text.length*(mode==='utf16'?2:1)};
    },
    ExportRCJ:async(j,mode)=>{
      calls.push(['ExportRCJ',copy(j),mode]);
      if(window.__rcjCancel)return null;
      if(window.__rcjExists)throw Error('a file with that name already exists; ZTG Copy does not replace files, so choose a new name');
      return {path:'D:\\Exports\\<img src=x onerror="window.__pwned=true">.rcj',encoding:mode==='utf16'?'UTF-16LE with byte order mark':'ASCII (plain bytes, the same in ANSI and UTF-8)',bytes:96,check:'No credentials are written. ZTG Copy does not run Robocopy for this; to have the installed Robocopy check the file, run robocopy /JOB:FILE /QUIT yourself.'};
    },
    Start:async(j,data)=>{
      calls.push(['Start',copy(j),data]);
      if(state.running)throw Error('operation already running');
      if(!describe(j).can_run)throw Error('job is not enabled');
      if(data && approved!==JSON.stringify(j))throw Error('preview required');
      active=copy(j);approved='';ticks=0;state={id:++sequence,running:true,mode:data?'run':'plan',phase:'Previewing',started:new Date().toISOString(),messages:['preview...'],phases:[{name:'preview',summary:copy(summary)}]};return sequence;
    },
    Status:async()=>{
      if(state.running && ++ticks>=3 && !window.__holdRun){
        const failed=state.mode==='run' && window.__failNextCopy;
        const outcome=failed?'failed':state.mode==='plan'?'planned':'copied';
        const copyPhase={name:'copy',process:{native_exit:failed?8:1},summary:copy(summary),...(failed?{diagnostics:copy(diagnostics)}:{})};
        const verifyPhase={name:'convergence',process:{native_exit:failed?1:0},summary:{...copy(summary),files:{...summary.files,copied:failed?1:0},bytes:{...summary.bytes,copied:failed?1024:0}}};
        state={...state,running:false,phase:outcome,messages:[...state.messages,outcome],record:{schema:1,job:active,mode:state.mode,state:outcome,normalized_exit:failed?2:0,started:state.started,finished:new Date().toISOString(),directory:'C:\\records\\2026-10-22\\0123456789abcdef01234567',phases:[{name:'preview',process:{native_exit:1},summary:copy(summary)},...(state.mode==='run'?[copyPhase,verifyPhase]:[])]}};
        if(state.mode==='plan')approved=JSON.stringify(active);
        if(failed)window.__failNextCopy=false;
      }
      return copy(state);
    },
    Stop:async()=>{calls.push(['Stop']);state={...state,running:false,phase:'canceled',record:{job:active,state:'canceled',normalized_exit:3,mode:state.mode,finished:new Date().toISOString(),phases:[]}};approved='';},
    Templates:async()=>templates.map(t=>({template:copy(t),view:describe(t.job)})),
    SaveTemplate:async(id,description,j,ask)=>{calls.push(['SaveTemplate',id]);templates.push({schema:1,catalog_version:1,id,description,job:ask?{...copy(j),source:'{source}',destination:'{destination}'}:copy(j),fields:ask?[{name:'source',kind:'path',description:'Source folder'},{name:'destination',kind:'path',description:'Destination folder'}]:[]});},
    ResolveTemplate:async(id,values)=>{calls.push(['ResolveTemplate',id,values]);const t=templates.find(t=>t.id===id);return describe({...t.job,source:values.source||t.job.source,destination:values.destination||t.job.destination});},
    InspectTemplate:async()=>({template:{schema:1,catalog_version:1,id:'imported',description:'<img src=x onerror="window.__pwned=true">',job:{...base,source:'C:\\Imported',destination:'D:\\Archive',options:['/MIR']}},view:{can_run:false,restriction:'Destructive option requires a later safety slice'}}),
    AcceptTemplate:async(t,accepted)=>{calls.push(['AcceptTemplate',accepted]);if(!accepted)throw Error('acceptance required');templates.push(copy(t));},
    ExportTemplate:async id=>{calls.push(['ExportTemplate',id]);return 'D:\\'+id+'.json';},
    History:async()=>copy(history),
    HistoryRecord:async id=>{calls.push(['HistoryRecord',id]);return {schema:1,mode:'run',state:'failed',normalized_exit:2,job:{...base,source:'C:\\History',destination:'D:\\Archive'},started:'2026-10-22T12:00:00Z',finished:'2026-10-22T12:01:00Z',phases:[{name:'help',process:{native_exit:16,in_job_before_resume:true}},{name:'copy',process:{native_exit:8,in_job_before_resume:true},diagnostics:copy(diagnostics)},{name:'convergence',process:{native_exit:0,in_job_before_resume:false}}]};},
    HistoryJob:async id=>{calls.push(['HistoryJob',id]);return describe({...base,source:'C:\\History',destination:'D:\\Archive'});},
    RevealRecord:async id=>calls.push(['RevealRecord',id])
  }}};
})();
