import {createRequire} from 'node:module';
import {createServer} from 'node:http';
import {readFile,mkdir} from 'node:fs/promises';
import {join,extname,resolve} from 'node:path';
import {fileURLToPath} from 'node:url';
import assert from 'node:assert/strict';
const {chromium}=createRequire(import.meta.url)('playwright');
const here=fileURLToPath(new URL('.',import.meta.url));
const frontend=resolve(here,'../frontend');
const screenshots=process.argv[2]||join(here,'screenshots');
await mkdir(screenshots,{recursive:true});
const catalogue=JSON.parse(await readFile(join(here,'catalog.json'),'utf8'));
const server=createServer(async(req,res)=>{
  const pathname=new URL(req.url,'http://local').pathname;
  if(pathname.startsWith('/wails/')){res.setHeader('content-type','text/javascript');res.end('');return;}
  const file=resolve(frontend,'.'+(pathname==='/'?'/index.html':pathname));
  if(!file.startsWith(frontend+'/')){res.writeHead(403).end();return;}
  try{const data=await readFile(file);res.setHeader('content-type',({'.js':'text/javascript','.css':'text/css','.html':'text/html','.png':'image/png'})[extname(file)]||'application/octet-stream');res.end(data);}
  catch{res.writeHead(404).end();}
}).listen(0,'127.0.0.1');
await new Promise(resolve=>server.once('listening',resolve));
const browser=await chromium.launch(process.env.CHROMIUM?{executablePath:process.env.CHROMIUM}:undefined);
async function screenshot(page,name){
  const data=await page.screenshot({path:join(screenshots,name),fullPage:true});
  const encoded=data.toString('base64');
  for(let offset=0;offset<encoded.length;offset+=16000)console.log('COPY_GUI_SCREENSHOT '+name+' '+offset+' '+encoded.slice(offset,offset+16000));
}
try{
  for(const scheme of ['dark','light']){
    const page=await browser.newPage({viewport:{width:1440,height:1050},colorScheme:scheme});
    const errors=[];page.on('pageerror',error=>errors.push(String(error)));
    let requests=0;
    await page.route('https://api.github.com/**',route=>{requests++;return route.fulfill({json:[{tag_name:'copy-v0.2.0',published_at:'2026-10-22T00:00:00Z',assets:[{name:'SHA256SUMS.txt',state:'uploaded',size:20},{name:'ZTG-Copy-0.2.0-x64.msi',state:'uploaded',size:20}]}]});});
    await page.addInitScript(data=>{window.__catalog=data;},catalogue);
    await page.addInitScript({path:join(here,'mock.js')});
    await page.goto('http://127.0.0.1:'+server.address().port);
    await page.waitForFunction(()=>document.getElementById('version').textContent==='0.1.0-preview');
    assert.equal(requests,0,'no background update checks');
    await page.click('[data-browse="source"]');await page.click('[data-browse="destination"]');
    await page.waitForFunction(()=>!document.getElementById('preview').disabled);
    await page.fill('#job-name','Quarterly file archive');
    await page.waitForTimeout(200);
    assert.match(await page.textContent('#command'),/SystemDirectory/);
    await screenshot(page,'copy-designer-'+scheme+'.png');
    await page.locator('.option-chip').filter({hasText:'/MT:8'}).click();
    await page.fill('#option-value','16');await page.click('#modal-submit');
    await page.waitForFunction(()=>document.getElementById('selected-options').textContent.includes('/MT:16'));
    await page.click('#preview');
    await page.waitForSelector('#preview-actions:not([hidden])');
    assert.equal(await page.textContent('#files-total'),'12');
    await page.click('#start-copy');await page.click('#modal-submit');
    await page.waitForFunction(()=>document.getElementById('phase').textContent==='Copy complete');
    assert.equal(await page.textContent('#outcome'),'Passed');
    const starts=await page.evaluate(()=>window.__calls.filter(c=>c[0]==='Start'));
    assert.equal(starts.length,2);assert.equal(starts[0][2],false);assert.equal(starts[1][2],true);
    if(scheme==='dark')await screenshot(page,'copy-activity-dark.png');
    await page.click('#open-latest');
    await page.click('#back-design');
    await page.click('#save-template');await page.fill('#template-name','quarterly-copy');await page.fill('#template-description','<img src=x onerror="window.__pwned=true">');await page.click('#modal-submit');
    await page.click('[data-view="templates"]');
    const card=page.locator('.template-card').filter({hasText:'quarterly-copy'});
    await card.getByRole('button',{name:'Use template',exact:true}).click();
    await page.fill('#template-field-source','C:\\Finance');await page.fill('#template-field-destination','D:\\Finance');await page.click('#modal-submit');
    await page.waitForFunction(()=>document.getElementById('source').value==='C:\\Finance');
    assert.equal(await page.locator('#preview-actions').isVisible(),false,'template does not reuse preview approval');
    await page.click('#import-command');await page.fill('#import-command-text','robocopy C:\\Data D:\\Archive /E /FUTURE:9');await page.click('#modal-submit');
    await page.waitForFunction(()=>document.getElementById('selected-options').textContent.includes('/FUTURE:9'));
    assert.equal(await page.locator('#preview').isDisabled(),true);
    await page.click('[data-view="templates"]');await page.click('#import-template');
    assert.equal(await page.locator('#modal-body img').count(),0);
    await page.click('#modal-cancel');
    assert.equal(await page.evaluate(()=>window.__calls.filter(c=>c[0]==='AcceptTemplate').length),0);
    await page.click('#import-template');await page.click('#modal-submit');
    await page.waitForFunction(()=>window.__calls.some(c=>c[0]==='AcceptTemplate'&&c[1]===true));
    assert.equal(await page.locator('#templates-list img').count(),0);
    await page.click('[data-view="history"]');
    await page.waitForSelector('#history-list tr');
    assert.equal(await page.locator('#history-list img').count(),0);
    await page.locator('#history-list').getByRole('button',{name:'Load job'}).click();
    await page.waitForFunction(()=>document.getElementById('source').value==='C:\\History');
    await page.click('#preview');await page.waitForSelector('#stop:not([disabled])');await page.click('#stop');
    await page.waitForFunction(()=>document.getElementById('phase').textContent==='Operation stopped');
    assert.equal(await page.locator('#preview-actions').isVisible(),false);
    await page.selectOption('#theme','light');await page.reload();await page.waitForFunction(()=>document.getElementById('version').textContent==='0.1.0-preview');
    assert.equal(await page.getAttribute('html','data-theme'),'light');
    await page.selectOption('#theme','dark');assert.equal(await page.getAttribute('html','data-theme'),'dark');
    assert.equal(await page.evaluate(()=>window.__pwned),undefined);
    await page.click('#check-updates');
    await page.waitForFunction(()=>document.getElementById('ztg-update-dialog')?.textContent.includes('Update available: 0.2.0'));
    await page.getByRole('button',{name:'View release and downloads'}).click();
    assert.ok(await page.evaluate(()=>window.__calls.some(c=>c[0]==='BrowserOpenURL'&&c[1]==='https://github.com/zerotrustgarage/tools/releases/tag/copy-v0.2.0')));
    await page.getByRole('button',{name:'Close',exact:true}).click();
    assert.equal(requests,1);
    await page.click('#win-min');await page.click('#win-max');await page.click('#win-close');
    assert.deepEqual(errors,[]);
    console.log('PASS '+scheme+': designer, option editing, preview, confirmed copy, stop, templates, history, safe text, themes and explicit updates');
    await page.close();
  }
}finally{await browser.close();await new Promise(resolve=>server.close(resolve));}
