(() => {
  const calls = window.__calls = [];
  const copy = value => JSON.parse(JSON.stringify(value));
  const base = {schema:1,source:'',destination:'',options:['/E','/MT:8','/DCOPY:DAT','/R:2','/W:5','/XJ']};
  const templates = [{schema:1,catalog_version:1,id:'sample',description:'<img src=x onerror="window.__pwned=true">',fields:[{name:'source',kind:'path',description:'Source folder'},{name:'destination',kind:'path',description:'Destination folder'}],job:{...base,source:'{source}',destination:'{destination}'}}];
  let state={id:0,running:false,messages:[],phase:''}, sequence=0, ticks=0, active, approved='';
  const summary={directories:{total:2,copied:0,skipped:2,mismatch:0,failed:0,extras:0},files:{total:12,copied:12,skipped:0,mismatch:0,failed:0,extras:0},bytes:{total:8388608,copied:8388608,skipped:0,mismatch:0,failed:0,extras:0}};
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
    Describe:async j=>describe(j),
    PickDirectory:async kind=>kind==='source'?'C:\\Company Data':'D:\\Archive',
    ChooseRecordsDirectory:async()=>{calls.push(['ChooseRecordsDirectory']);return 'D:\\Copy records';},
    Explain:async text=>{calls.push(['Explain',text]);return describe({schema:1,source:'C:\\Data',destination:'D:\\Archive',options:text.includes('/FUTURE')?['/E','/FUTURE:9']:['/E']});},
    ImportJob:async()=>describe({...base,source:'C:\\Imported',destination:'D:\\Imported'}),
    ExportJob:async j=>{calls.push(['ExportJob',j]);return 'D:\\copy-job.json';},
    Start:async(j,data)=>{
      calls.push(['Start',copy(j),data]);
      if(state.running)throw Error('operation already running');
      if(!describe(j).can_run)throw Error('job is not enabled');
      if(data && approved!==JSON.stringify(j))throw Error('preview required');
      active=copy(j);approved='';ticks=0;state={id:++sequence,running:true,mode:data?'run':'plan',phase:'Previewing',started:new Date().toISOString(),messages:['preview...'],phases:[{name:'preview',summary:copy(summary)}]};return sequence;
    },
    Status:async()=>{
      if(state.running && ++ticks>=3){
        const outcome=state.mode==='plan'?'planned':'copied';
        state={...state,running:false,phase:outcome,messages:[...state.messages,outcome],record:{schema:1,job:active,mode:state.mode,state:outcome,normalized_exit:0,started:state.started,finished:new Date().toISOString(),directory:'C:\\records\\2026-10-22\\0123456789abcdef01234567',phases:[{name:'preview',summary:copy(summary)},...(state.mode==='run'?[{name:'copy',summary:copy(summary)},{name:'convergence',summary:{...copy(summary),files:{...summary.files,copied:0},bytes:{...summary.bytes,copied:0}}}]:[])]}};
        if(state.mode==='plan')approved=JSON.stringify(active);
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
    HistoryJob:async id=>{calls.push(['HistoryJob',id]);return describe({...base,source:'C:\\History',destination:'D:\\Archive'});},
    RevealRecord:async id=>calls.push(['RevealRecord',id])
  }}};
})();
