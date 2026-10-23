import {createRequire} from 'node:module';
import {writeFile,readFile,mkdir} from 'node:fs/promises';
import {join} from 'node:path';
import assert from 'node:assert/strict';
const {chromium}=createRequire(import.meta.url)('playwright');
const [port,root,out]=process.argv.slice(2);
if(!port||!root||!out)throw Error('native.mjs requires CDP port, fixture root and screenshot directory');
await mkdir(out,{recursive:true});
let browser,lastConnectionError;
const watchdog=setTimeout(()=>{console.error('Native desktop UI test exceeded four minutes');process.exit(1);},240000);
for(let attempt=0;attempt<45;attempt++){
  try{browser=await chromium.connectOverCDP('http://127.0.0.1:'+port,{timeout:1000});break;}
  catch(error){lastConnectionError=error;if(attempt===0)console.log('CDP first attempt: '+error.message);await new Promise(resolve=>setTimeout(resolve,1000));}
}
if(!browser){
  console.error('CDP last attempt: '+lastConnectionError?.stack);
  try{const response=await fetch('http://127.0.0.1:'+port+'/json/version');console.error('CDP version: '+await response.text());}catch(error){console.error('CDP HTTP: '+error);}
  clearTimeout(watchdog);throw Error('Windows WebView2 debugging endpoint did not become ready');
}
console.log('Native desktop: CDP connected');
try{
  const context=browser.contexts()[0];
  let page=context.pages()[0];
  if(!page)page=await context.waitForEvent('page',{timeout:15000});
  page.setDefaultTimeout(15000);
  const errors=[];page.on('pageerror',error=>errors.push(String(error)));
  await page.waitForFunction(()=>window.go?.main?.App && document.getElementById('version')?.textContent.includes('0.1.0'),null,{timeout:30000});
  const source=join(root,'source space'),destination=join(root,'destination');
  await page.fill('#source',source);await page.fill('#destination',destination);await page.fill('#job-name','Windows desktop test');
  await page.waitForFunction(()=>!document.getElementById('preview').disabled);
  await page.click('#preview');await page.waitForSelector('#preview-actions:not([hidden])',{timeout:30000});
  await assert.rejects(()=>readFile(join(destination,'café-日本語-😀.txt')),{code:'ENOENT'});
  await page.click('#start-copy');await page.click('#modal-submit');
  await page.waitForFunction(()=>document.getElementById('phase').textContent==='Copy complete',null,{timeout:30000});
  assert.equal(await readFile(join(destination,'café-日本語-😀.txt'),'utf8'),'Desktop copy 日本語-😀');
  assert.equal(await page.textContent('#outcome'),'Passed');
  await page.click('#back-design');await page.click('#save-template');
  await page.fill('#template-name','desktop-copy');await page.fill('#template-description','Real Windows desktop fixture');await page.click('#modal-submit');
  await page.click('[data-view="templates"]');
  await page.locator('.template-card').filter({hasText:'desktop-copy'}).getByRole('button',{name:'Use template',exact:true}).click();
  await page.fill('#template-field-source',source);await page.fill('#template-field-destination',destination);await page.click('#modal-submit');
  await page.waitForFunction(()=>document.getElementById('source').value.length>0);
  assert.equal(await page.locator('#preview-actions').isVisible(),false);
  await page.click('[data-view="history"]');await page.waitForSelector('#history-list tr');
  assert.ok(await page.locator('#history-list tr').count()>=2);
  await page.locator('#history-list tr').first().getByRole('button',{name:'Load job'}).click();
  await page.waitForFunction(()=>!document.getElementById('view-design').hidden);
  for(const theme of ['light','dark']){
    await page.selectOption('#theme',theme);
    await page.waitForTimeout(100);
    const data=await page.screenshot({path:join(out,'copy-native-'+theme+'.png')});
    const encoded=data.toString('base64');
    for(let offset=0;offset<encoded.length;offset+=16000)console.log('COPY_GUI_SCREENSHOT copy-native-'+theme+'.png '+offset+' '+encoded.slice(offset,offset+16000));
  }
  await writeFile(join(source,'slow.bin'),Buffer.alloc(8<<20,0x5a));
  for(const token of ['IORATE','NOOFFLOAD','NOCLONE']){
    await page.fill('#option-search',token);
    const row=page.locator('.catalog-row').filter({has:page.getByText('/'+token,{exact:true})});
    await row.getByRole('button',{name:'Add',exact:true}).click();
    if(token==='IORATE')await page.fill('#option-value','1048576');
    await page.click('#modal-submit');
    await page.waitForTimeout(200);
  }
  await page.waitForFunction(()=>!document.getElementById('preview').disabled);
  await page.click('#preview');await page.waitForSelector('#preview-actions:not([hidden])',{timeout:30000});
  await page.click('#start-copy');await page.click('#modal-submit');
  await page.waitForFunction(()=>document.getElementById('phase').textContent==='Copying',null,{timeout:30000});
  await page.click('#stop');
  await page.waitForFunction(()=>document.getElementById('phase').textContent==='Operation stopped',null,{timeout:15000});
  const status=await page.evaluate(()=>window.go.main.App.Status());
  assert.equal(status.running,false);assert.equal(status.record.normalized_exit,3);assert.equal(status.record.state,'canceled');
  assert.equal(await page.locator('#preview-actions').isVisible(),false);
  assert.deepEqual(errors,[]);
  console.log('COPY_NATIVE_GUI '+JSON.stringify({passed:true,real_wails_bridge:true,preview_unchanged:true,unicode_content_equal:true,templates:true,history:true,stop_canceled:true,themes:['light','dark']}));
}finally{clearTimeout(watchdog);await browser.close();}
