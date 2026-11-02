// The script every page of the site carries (plain ES5, no dependencies): search over guides, operations and models, and the language tabs.
// docs-site/client.test.mjs runs it against a small stand-in for the DOM, so what it promises is tested without a browser.

/** `up` is the path from the page to the site's root ("" or "../"), where search.json lives. */
export function clientScript(up) {
  return `(function(){
var KEY='payrudder-docs-lang';
function remembered(){try{return localStorage.getItem(KEY)}catch(e){return null}}
function remember(v){try{localStorage.setItem(KEY,v)}catch(e){}}

// Search. Every word must be in the title or the text; a hit whose title has all the words comes first, an exact title before those; ties keep the index order (guides, operations, models).
function rank(idx,query){
  var q=query.trim().toLowerCase();if(q.length<2)return [];
  var words=q.split(/\\s+/),out=[];
  idx.forEach(function(e,i){
    var title=e.title.toLowerCase();
    if(!words.every(function(w){return (title+' '+e.text.toLowerCase()).indexOf(w)>=0}))return;
    var s=0;
    if(words.every(function(w){return title.indexOf(w)>=0}))s+=2;
    if(title===q)s+=3;
    out.push({e:e,s:s,i:i});
  });
  out.sort(function(a,b){return b.s-a.s||a.i-b.i});
  return out.slice(0,12).map(function(x){return x.e});
}
var q=document.getElementById('q'),hits=document.getElementById('hits'),idx=null;
function load(){return idx?Promise.resolve(idx):fetch('${up}search.json').then(function(r){return r.json()}).then(function(j){idx=j;return j})}
q.addEventListener('input',function(){
  hits.innerHTML='';
  if(q.value.trim().length<2)return;
  load().then(function(j){rank(j,q.value).forEach(function(e){
    var li=document.createElement('li'),a=document.createElement('a'),k=document.createElement('small');
    a.href='${up}'+e.url;a.textContent=e.title;k.textContent=' '+(e.kind||'');
    li.appendChild(a);li.appendChild(k);hits.appendChild(li);
  })});
});

// Language tabs: one choice for every group of code on the page, kept in this browser. A group that lacks the chosen language shows its first.
var groups=document.querySelectorAll('.tabs');
function show(lang){groups.forEach(function(g){
  var tabs=g.querySelectorAll('[role=tab]'),panels=g.querySelectorAll('[role=tabpanel]'),pick=tabs[0].getAttribute('data-lang');
  tabs.forEach(function(t){if(t.getAttribute('data-lang')===lang)pick=lang});
  tabs.forEach(function(t){t.setAttribute('aria-selected',String(t.getAttribute('data-lang')===pick))});
  panels.forEach(function(p){p.hidden=p.getAttribute('data-lang')!==pick});
})}
groups.forEach(function(g){g.querySelectorAll('[role=tab]').forEach(function(t){
  t.addEventListener('click',function(){var l=t.getAttribute('data-lang');remember(l);show(l)});
})});
show(remembered());

// For the tests only: the search ranking.
window.payrudderDocs={rank:rank};
})();`;
}
