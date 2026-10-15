// Run from platform/: playwright-cli -s=audit run-code --filename scripts/check-ui.js
// Uses synthetic API responses; no application data is written.
async page => {
 let uploaded=false,hideContacts=true,holdNews=false,releaseNews,newsStarted;
 const newsGate=new Promise(resolve=>releaseNews=resolve),newsPending=new Promise(resolve=>newsStarted=resolve);
 await page.unroute('**/api/v1/**');
 await page.route('**/api/v1/**', async route => {
  const req=route.request(), url={pathname:req.url().split('?')[0]};
  const org={id:1,slug:'audit-school',kind:'school',active:true,name:{ru:'Школа аудита',kk:'Тест мектебі'},profile:{},hidden_sections:[]};
  const material={id:11,organization_id:1,kind:'news',published:true,position:0,translations:{ru:{title:'Исходный заголовок',body:'<p>Исходный текст</p>'},kk:{title:'Тест',body:''}},updated_at:'2026-10-13T00:00:00Z',files:[]};
  const list=items=>({items,total:items.length,page:1,page_size:20});
  let data;
  if(url.pathname.endsWith('/site')) data={central:false,domain:'school.localhost',organization:org,menu:[{key:'home',label:{ru:'Главная',kk:'Басты бет'},visible:true,parent:''},{key:'news',label:{ru:'Новости',kk:'Жаңалықтар'},visible:true,parent:''},{key:'contacts',label:{ru:'Контакты',kk:'Байланыс'},visible:!hideContacts,parent:''}]};
  else if(url.pathname.endsWith('/auth/me')) data={id:1,organization_id:1,login:'audit-school',must_change:false,csrf:'test-csrf'};
  else if(url.pathname.endsWith('/admin/organizations')) data=list([org]);
  else if(url.pathname.endsWith('/admin/uploads')) {uploaded=true;data={id:'audit-file',name:'audit.pdf',mime:'application/pdf',width:0,height:0,caption:{},position:0};}
  else if(req.method()==='PUT'&&url.pathname.endsWith('/admin/content/11')) {data={...material,...req.postDataJSON()};if(data.translations.ru.title!=='Несохранённый заголовок'||!data.translations.ru.body.replaceAll('&nbsp;',' ').includes('Несохранённый текст'))throw new Error('Uploaded file erased the draft');}
  else if(url.pathname.endsWith('/admin/content')) {
   if(holdNews&&req.url().includes('kind=news')){holdNews=false;newsStarted();await newsGate;}
   if(req.url().includes('kind=documents')){material.kind='documents';material.translations.ru.title='Документ аудита';}
   data=list(uploaded?[]:[material]);
  }
  else if(url.pathname.endsWith('/content/99')) data={...material,kind:'documents'};
  else if(url.pathname.endsWith('/content')) data={...list([material]),total:40};
  else {await route.fulfill({status:404,json:{error:'Unexpected test request '+url.pathname}});return;}
  await route.fulfill({json:data});
 });
 await page.goto('http://school.localhost:5173/admin/');
 await page.getByRole('button',{name:'РУС',exact:true}).click();
 await page.getByRole('button',{name:'Редактировать',exact:true}).click();

 await page.getByRole('button',{name:'РУС',exact:true}).last().click();
 await page.getByRole('textbox',{name:'Заголовок',exact:true}).fill('Несохранённый заголовок');
 await page.locator('.ql-editor').fill('Несохранённый текст');
 await page.getByLabel('Добавить файл',{exact:true}).setInputFiles('fixtures/demo/document.pdf');
 await page.getByRole('link',{name:'audit.pdf',exact:true}).waitFor();
 if(await page.getByRole('textbox',{name:'Заголовок',exact:true}).inputValue()!=='Несохранённый заголовок')throw new Error('Title lost after upload');
 if(!(await page.locator('.ql-editor').innerText()).includes('Несохранённый текст'))throw new Error('Body lost after upload');
 await page.getByRole('button',{name:'Сохранить черновик / снять с публикации',exact:true}).click();
 await page.getByText('Черновик сохранён',{exact:true}).waitFor();
 await page.screenshot({path:'output/playwright/audit-upload-preserves-draft.png'});
 await page.goto('http://school.localhost:5173/contacts');
 await page.getByRole('alert').filter({hasText:'Страница не найдена'}).waitFor();
 if(await page.locator('html').getAttribute('lang')!=='ru')throw new Error('Persisted language missing from html');
 await page.screenshot({path:'output/playwright/audit-hidden-contacts.png'});
 hideContacts=false;
 await page.goto('http://school.localhost:5173/news');
 await page.locator('.pagination').waitFor();
 await page.getByRole('link',{name:'Контакты',exact:true}).click();
 await page.getByRole('heading',{name:'Контактная информация',exact:true}).waitFor();
 if(await page.locator('.pagination').count())throw new Error('Contacts inherited news pagination');
 await page.goto('http://school.localhost:5173/news/99');
 await page.getByRole('alert').filter({hasText:'Страница не найдена'}).waitFor();
 uploaded=false;
 await page.goto('http://school.localhost:5173/admin/documents');
 await page.getByRole('cell',{name:'Документ аудита',exact:true}).waitFor();
 const navigate=path=>page.evaluate(path=>{history.pushState({},'',path);dispatchEvent(new PopStateEvent('popstate'));},path);
 holdNews=true;
 await navigate('/admin/');
 await newsPending;
 await navigate('/admin/documents');
 await page.getByRole('heading',{name:'Документы',exact:true}).waitFor();
 await page.getByRole('cell',{name:'Документ аудита',exact:true}).waitFor();
 const oldResponse=page.waitForResponse(response=>response.url().includes('/admin/content?')&&response.url().includes('kind=news'));
 releaseNews();await oldResponse;
 await page.evaluate(()=>new Promise(resolve=>requestAnimationFrame(()=>requestAnimationFrame(resolve))));
 if(await page.getByRole('cell',{name:'Исходный заголовок',exact:true}).count())throw new Error('Late news response replaced documents');
 await page.getByRole('cell',{name:'Документ аудита',exact:true}).waitFor();
 await page.unroute('**/api/v1/**');
 console.log('PASS: draft/file preservation, save payload, menu visibility, language, pagination and detail kind');
}
