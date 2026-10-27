import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { runInNewContext } from 'node:vm';

const script = readFileSync(new URL('../assets/scripts/hyperchi-prefetch.js', import.meta.url), 'utf8');
class Element {
  constructor(attrs = {}, parent = null, tag = 'A') { this.attrs = attrs; this.parentElement = parent; this.tagName = tag; this.isConnected = true; }
  get attributes() { return Object.entries(this.attrs).map(([name,value]) => ({name,value})); }
  get id() { return this.attrs.id || ''; }
  get target() { return this.attrs.target || ''; }
  hasAttribute(name) { return Object.hasOwn(this.attrs,name); }
  getAttribute(name) { return this.attrs[name] ?? null; }
  matches(selector) { return this.tagName === 'A' && this.hasAttribute('data-hc-prefetch'); }
  closest(selector) {
    if (selector === '[data-hc-prefetch="off"]') {
      for (let n=this;n;n=n.parentElement) if (n.attrs['data-hc-prefetch']==='off') return n;
      return null;
    }
    return this.matches(selector) ? this : this.parentElement?.closest(selector);
  }
  contains(node) { return node===this; }
}
function setup(t, {fetcher, saveData=false, slow=false, offline=false}={}) {
  const listeners=new Map();
  const timers=new Map(); let timerID=0;
  const elements=[]; const calls=[];
  const addEventListener=(name,fn) => { if (!listeners.has(name)) listeners.set(name,[]); listeners.get(name).push(fn); };
  const fire=(name,event={}) => { for(const fn of listeners.get(name)||[]) fn(event); };
  const body=new Element({},null,'BODY');
  const doc={readyState:'complete',hidden:false,body,documentElement:body,baseURI:'https://site.test/',addEventListener,
    querySelectorAll:()=>elements,querySelector:s=>s==='body'?body:elements.find(e=>'#'+e.id===s)||null};
  let io;
  class IntersectionObserver {
    constructor(callback){this.callback=callback;this.observed=new Set();io=this;}
    observe(el){this.observed.add(el);}
    disconnect(){this.observed.clear();}
    emit(el,visible){this.callback([{target:el,isIntersecting:visible}]);}
  }
  const connection={saveData,effectiveType:slow?'2g':'4g',addEventListener};
  const window={addEventListener,IntersectionObserver,matchMedia:()=>({matches:false,addEventListener}),htmx:{config:{prefix:'data-hx-',implicitInheritance:false}}};
  runInNewContext(script,{window,document:doc,navigator:{onLine:!offline,connection},location:{origin:'https://site.test',href:'https://site.test/'},URL,AbortController,queueMicrotask,
    MutationObserver:class {observe(){} disconnect(){}},IntersectionObserver,
    setTimeout:(fn,delay)=>{timers.set(++timerID,{fn,delay});return timerID;},clearTimeout:id=>timers.delete(id),
    fetch:(url,options)=>{calls.push({url,options});return fetcher?fetcher(url,options):Promise.resolve(new Response('hello',{headers:{'Content-Type':'text/html','Cache-Control':'max-age=60'}}));}});
  const add=(attrs={},parent=body)=>{const el=new Element({'href':'/next','data-hc-prefetch':'hover',...attrs},parent);elements.push(el);return el;};
  const tick=async()=>{for(const [id,timer] of [...timers]) if(timer.delay===100){timers.delete(id);timer.fn();}await new Promise(setImmediate);await new Promise(setImmediate);};
  const enter=el=>fire('pointerover',{target:el,relatedTarget:null,pointerType:'mouse'});
  const leave=el=>fire('pointerout',{target:el,relatedTarget:null});
  t.after(()=>window.HyperChiPrefetch.stop());
  return {window,doc,connection,calls,add,tick,enter,leave,fire,get io(){return io;}};
}

test('hover waits for intent, deduplicates, and leaving cancels before fetch',async t=>{
  const e=setup(t);const a=e.add();e.enter(a);assert.equal(e.calls.length,0);e.leave(a);await e.tick();assert.equal(e.calls.length,0);
  e.enter(a);await e.tick();assert.equal(e.calls.length,1);assert.equal(e.calls[0].options.credentials,'same-origin');assert.equal(e.calls[0].options.redirect,'error');
  e.leave(a);e.enter(a);await e.tick();assert.equal(e.calls.length,1);
});
test('eligibility rejects off, external, unsafe, download and dynamic requests',async t=>{
  const e=setup(t);
  for(const attrs of [
    {href:'https://other.test/'},{href:'javascript:alert(1)'},{href:'#anchor'},{href:'/'},{href:'https://user:pass@site.test/a'},
    {download:''},{target:'_blank'},{ping:'/audit'},{'data-hc-prefetch':'off'},{'hx-post':'/delete'},
    {'hx-get':'/data','hx-headers':'{"X-Private":"yes"}'},{'hx-get':'/data','hx-include':'#form'},
  ]) e.enter(e.add(attrs));
  e.enter(e.add({},new Element({'data-hc-prefetch':'off'})));
  await e.tick();assert.equal(e.calls.length,0);
});
test('htmx 4 requests include correct fragment and explicit inheritance headers',async t=>{
  const e=setup(t);const target=e.add({id:'result'});target.tagName='DIV';
  const a=e.add({id:'link','hx-get':'/fragment','hx-target':'#result','data-htmx-powered':'true'});
  e.enter(a);await e.tick();
  const h=e.calls[0].options.headers;
  assert.equal(h['HX-Request'],'true');assert.equal(h['HX-Request-Type'],'partial');assert.equal(h['HX-Source'],'a#link');assert.equal(h['HX-Target'],'div#result');
  const parent=new Element({'hx-boost:inherited':'true'},e.doc.body,'NAV');
  const boosted=e.add({href:'/boost','data-htmx-powered':'true'},parent);e.enter(boosted);await e.tick();
  assert.equal(e.calls[1].options.headers['HX-Boosted'],'true');assert.equal(e.calls[1].options.headers['HX-Request-Type'],'full');
});
test('viewport observes new content and cancels in-flight requests on exit',async t=>{
  const e=setup(t,{fetcher:(_,o)=>new Promise((_,reject)=>o.signal.addEventListener('abort',()=>reject(new Error('abort'))))});
  const a=e.add({'data-hc-prefetch':'viewport'});e.window.HyperChiPrefetch.refresh();assert(e.io.observed.has(a));
  e.io.emit(a,true);await e.tick();assert.equal(e.calls.length,1);e.io.emit(a,false);assert(e.calls[0].options.signal.aborted);
  const b=e.add({href:'/new','data-hc-prefetch':'viewport'});e.fire('htmx:after:swap');await e.tick();assert(e.io.observed.has(b));
});
test('caps concurrency and drains queued work',async t=>{
  const completions=[];
  const e=setup(t,{fetcher:()=>new Promise(resolve=>completions.push(()=>resolve(new Response('ok',{headers:{'Content-Type':'text/html'}}))))});
  for(let i=0;i<4;i++) e.enter(e.add({href:`/page-${i}`}));
  await e.tick();assert.equal(e.calls.length,2);completions.shift()();await e.tick();assert.equal(e.calls.length,3);
});
for(const option of ['saveData','slow','offline']) test(`skips speculation on ${option}`,async t=>{
  const e=setup(t,{[option]:true});e.enter(e.add());await e.tick();assert.equal(e.calls.length,0);
});
test('hidden pages and mutations abort speculation; stopping is permanent',async t=>{
  const e=setup(t,{fetcher:(_,o)=>new Promise((_,reject)=>o.signal.addEventListener('abort',()=>reject(new Error('abort'))))});
  const a=e.add();e.enter(a);await e.tick();e.doc.hidden=true;e.fire('visibilitychange');await e.tick();assert(e.calls[0].options.signal.aborted);
  e.doc.hidden=false;e.window.HyperChiPrefetch.refresh();e.enter(a);await e.tick();
  e.fire('htmx:before:request',{detail:{ctx:{request:{method:'POST'}}}});assert(e.calls.at(-1).options.signal.aborted);
  e.window.HyperChiPrefetch.stop();e.enter(e.add({href:'/after-stop'}));await e.tick();assert.equal(e.calls.length,2);
});
test('invalid content and oversized bodies are aborted',async t=>{
  const e=setup(t,{fetcher:()=>Promise.resolve(new Response('big',{headers:{'Content-Type':'text/html','Content-Length':'2000000'}}))});
  e.enter(e.add());await e.tick();assert(e.calls[0].options.signal.aborted);
});

test('keyboard focus keeps interest after pointer exit',async t=>{
  const e=setup(t,{fetcher:(_,o)=>new Promise((_,reject)=>o.signal.addEventListener('abort',()=>reject(new Error('abort'))))});
  const a=e.add();e.enter(a);e.fire('focusin',{target:a,relatedTarget:null});await e.tick();
  e.leave(a);assert.equal(e.calls[0].options.signal.aborted,false);
  e.fire('focusout',{target:a,relatedTarget:null});assert.equal(e.calls[0].options.signal.aborted,true);
});
test('changed URLs abort the old speculative request',async t=>{
  const e=setup(t,{fetcher:(_,o)=>new Promise((_,reject)=>o.signal.addEventListener('abort',()=>reject(new Error('abort'))))});
  const a=e.add();e.enter(a);await e.tick();a.attrs.href='/changed';e.window.HyperChiPrefetch.refresh();
  assert(e.calls[0].options.signal.aborted);
});
