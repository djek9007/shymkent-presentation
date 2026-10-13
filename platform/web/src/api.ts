import { ref } from 'vue'
export const lang=ref<'kk'|'ru'>(localStorage.getItem('language')==='ru'?'ru':'kk')
export function language(value:'kk'|'ru'){lang.value=value;localStorage.setItem('language',value);document.documentElement.lang=value}
export function t(kk:string,ru:string){return lang.value==='kk'?kk:ru}
export let csrf=''
export function setCSRF(value:string){csrf=value}
export class APIError extends Error {constructor(public status:number,message:string,public fields:Record<string,string>={}){super(message)}}
export async function api<T=any>(path:string,options:RequestInit={}):Promise<T>{const headers=new Headers(options.headers);if(options.body&&!(options.body instanceof FormData))headers.set('Content-Type','application/json');if(options.method&&options.method!=='GET')headers.set('X-CSRF-Token',csrf);const response=await fetch('/api/v1'+path,{...options,headers,credentials:'same-origin'});if(!response.ok){const data=await response.json().catch(()=>({error:t('Серверге қосылу мүмкін емес','Ошибка сервера')}));throw new APIError(response.status,data.error,data.fields)};if(response.status===204)return undefined as T;return response.json()}
export interface Organization {id:number;slug:string;kind:string;active:boolean;name:Record<string,string>;profile:Record<string,string>;hidden_sections:string[]}
export interface Upload {width:number;height:number;id:string;name:string;mime:string;caption:Record<string,string>;position:number}
export interface Content {schedule_date?:string;id:number;organization_id:number;kind:string;published:boolean;position:number;translations:Record<string,{title:string;body:string}>;updated_at:string;files:Upload[]}
export interface List<T>{items:T[];total:number;page:number;page_size:number}
export const sections=[['about','Ұйым туралы','Об организации'],['administration','Әкімшілік','Администрация'],['teachers','Педагогтер','Педагоги'],['news','Жаңалықтар','Новости'],['documents','Құжаттар','Документы'],['gallery','Фотогалерея','Галерея'],['schedule','Кесте','Расписание'],['contacts','Байланыс','Контакты']]
export function sectionName(kind:string){if(kind==='banners')return t('Баннерлер','Баннеры');const s=sections.find(s=>s[0]===kind);return s?t(s[1],s[2]):kind}
export function translated(values:Record<string,string>){return values[lang.value]||t('Аударма әлі қосылмаған','Перевод ещё не добавлен')}
export interface MenuItem {key:string;label:Record<string,string>;visible:boolean;parent?:string;group?:boolean;url?:string}
export interface MenuNode extends MenuItem {children:MenuNode[]}
export function menuTree(items:MenuItem[],includeHidden=false):MenuNode[]{
 const children=(parent:string,depth:number):MenuNode[]=>depth>3?[]:items.filter(item=>(item.parent||'')===parent&&(includeHidden||item.visible)).map(item=>({...item,children:children(item.key,depth+1)})).filter(item=>includeHidden||!item.group||item.children.length>0)
 return children('',1)
}
