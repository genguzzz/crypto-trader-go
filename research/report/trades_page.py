"""Trade-by-trade page for one research/altbt run (Chinese, self-contained, sortable and filterable).

  python3 research/report/trades_page.py <altbt --out dir> <page name> "<title>"
  → docs/research/<page name>.html and web/public/research/<page name>.html (served at /research/…)
"""
import csv
import datetime as dt
import json
import os
import sys

HERE = os.path.dirname(os.path.abspath(__file__))
ROOT = os.path.abspath(os.path.join(HERE, "..", ".."))


def leg(reason):
    return "弱势做空" if "最弱" in reason else ("突破做空" if "跌破" in reason else "突破做多")


def main():
    src, name, title = sys.argv[1], sys.argv[2], sys.argv[3]
    s = json.load(open(os.path.join(src, "summary.json")))
    rows = []
    for t in csv.DictReader(open(os.path.join(src, "trades.csv"))):
        e, x, st, q = float(t["entry"]), float(t["exit"]), float(t["stop"]), float(t["qty"])
        risk = q * abs(e - st)
        rows.append(dict(inst=t["inst"].replace("-USDT-SWAP", ""), side=t["side"], leg=leg(t["reason"]),
                         tin=int(t["t_in"]), tout=int(t["t_out"]), entry=e, exit=x, stop=st,
                         stopd=abs(e - st) / e, notional=float(t["notional"]), margin=float(t["margin"]), lever=float(t["lever"]),
                         pnl=float(t["pnl"]), fees=float(t["fees"]), funding=float(t["funding"]),
                         R=float(t["pnl"]) / risk if risk > 0 else 0.0, kind=t["kind"], reason=t["reason"]))
    rows.sort(key=lambda r: r["tin"])
    data = {"title": title, "summary": s, "rows": rows,
            "generated": dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")}
    blob = json.dumps(data, ensure_ascii=False, separators=(",", ":")).replace("</", "<\\/")
    html = TEMPLATE.replace("__DATA__", blob)
    for base in (os.path.join(ROOT, "docs", "research"), os.path.join(ROOT, "web", "public", "research")):
        os.makedirs(base, exist_ok=True)
        open(os.path.join(base, name + ".html"), "w", encoding="utf-8").write(html)
    print(f"wrote {name}.html ({len(html) // 1024} KB, {len(rows)} trades)")


TEMPLATE = r"""<!doctype html>
<html lang="zh-CN"><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>逐笔交易记录</title>
<style>
.viz-root{color-scheme:light;--s0:#f4f3f0;--s1:#fcfcfb;--line:#e3e2de;--t1:#0b0b0b;--t2:#52514e;--t3:#7c7b76;--pos:#1a7f37;--neg:#c62828;--chip:#ecebe7}
@media (prefers-color-scheme:dark){.viz-root{color-scheme:dark;--s0:#111110;--s1:#1a1a19;--line:#2e2e2b;--t1:#fff;--t2:#c3c2b7;--t3:#8f8e86;--pos:#57c27a;--neg:#ef7d7d;--chip:#262624}}
*{box-sizing:border-box}body{margin:0}
.viz-root{background:var(--s0);color:var(--t1);font:14px/1.55 -apple-system,BlinkMacSystemFont,"PingFang SC","Noto Sans SC","Microsoft YaHei",sans-serif;min-height:100vh}
.wrap{max-width:1180px;margin:0 auto;padding:24px 16px 60px}
h1{font-size:22px;margin:0 0 4px}.meta{color:var(--t3);font-size:12px}
.tiles{display:grid;grid-template-columns:repeat(auto-fit,minmax(150px,1fr));gap:10px;margin:14px 0}
.tile{background:var(--s1);border:1px solid var(--line);border-radius:10px;padding:10px 12px}.tile .k{font-size:12px;color:var(--t3)}.tile .v{font-size:20px;font-weight:650;font-variant-numeric:tabular-nums}
.bar{display:flex;flex-wrap:wrap;gap:8px;align-items:center;margin:10px 0}
select,input{background:var(--s1);color:var(--t1);border:1px solid var(--line);border-radius:6px;padding:5px 8px;font-size:13px}
.card{background:var(--s1);border:1px solid var(--line);border-radius:10px;overflow:auto}
table{width:100%;border-collapse:collapse;font-size:13px}
th,td{padding:6px 8px;border-bottom:1px solid var(--line);white-space:nowrap;text-align:left}
th{color:var(--t3);font-weight:600;font-size:12px;cursor:pointer;position:sticky;top:0;background:var(--s1)}
td.n,th.n{text-align:right;font-variant-numeric:tabular-nums}
td.pos{color:var(--pos)}td.neg{color:var(--neg)}
tr.row{cursor:pointer}tr.row:hover td{background:var(--s0)}
tr.det td{white-space:normal;color:var(--t2);background:var(--s0);font-size:12px}
.chip{display:inline-block;font-size:11px;padding:0 6px;border-radius:999px;background:var(--chip);color:var(--t2)}
.legs{margin:8px 0 0}
</style></head>
<body><div class="viz-root"><div class="wrap">
<h1 id="title"></h1><div class="meta" id="meta"></div>
<div class="tiles" id="tiles"></div>
<div class="card" style="padding:10px 12px;margin-bottom:10px"><b>按腿和离场方式</b><div class="legs"><table id="legs"></table></div></div>
<div class="bar">
 <select id="f-leg"><option value="">全部腿</option></select>
 <select id="f-kind"><option value="">全部离场方式</option></select>
 <select id="f-res"><option value="">盈亏都看</option><option value="w">只看赚的</option><option value="l">只看亏的</option></select>
 <input id="f-inst" placeholder="币种，如 RAVE" size="12">
 <span class="meta" id="count"></span>
</div>
<div class="card"><table id="t"></table></div>
<p class="meta">点表头排序，点一行看开仓理由。时间为 UTC+8。离场方式：fill = 策略发出平仓（趋势腿跌回中轨）；time = 到持有时限平仓（弱势做空 72 小时）；stop = 打到止损。保证金是订单层为让强平价落在止损之外而自动降杠杆后的实际占用。表格合计只算已平仓交易；顶部“净利”还包含期末未平仓位的浮动盈亏。</p>
</div></div>
<script id="data" type="application/json">__DATA__</script>
<script>
(function(){
const D=JSON.parse(document.getElementById('data').textContent),S=D.summary,R=D.rows;
const $=id=>document.getElementById(id);
const h=(t,p,...k)=>{const e=document.createElement(t);for(const a in p||{}){if(a==='text')e.textContent=p[a];else if(a==='class')e.className=p[a];else e.setAttribute(a,p[a]);}k.forEach(x=>x!=null&&e.append(x));return e;};
const f=(x,d=0)=>(Math.abs(x)<1e-9?0:x).toLocaleString('zh-CN',{maximumFractionDigits:d,minimumFractionDigits:d});
const sg=(x,d=0)=>(x>0?'+':'')+f(x,d);
const tm=t=>{const d=new Date(t+8*3600e3);return d.toISOString().slice(5,16).replace('T',' ');};
const KIND={fill:'信号平仓',time:'到时平仓',stop:'止损',target:'止盈',liquidation:'强平'};
$('title').textContent=D.title;
$('meta').textContent=`${S.from} → ${S.to} · 入金 ${f(S.deposit)} U · 生成于 ${D.generated}`;
const pnl=R.map(r=>r.pnl),wins=pnl.filter(x=>x>0).length;
for(const [k,v] of [['净利',sg(S.net)+' U'],['交易笔数',f(R.length)],['胜率',f(wins/R.length*100)+'%'],['最大回撤',sg(S.max_drawdown)+' U'],['账户最低',f(S.min_equity)+' U'],['去掉最好 5 笔',sg(S.net_without_best5)+' U'],['平均名义',f(S.avg_notional)+' U'],['平均保证金',f(S.avg_margin)+' U']])
  $('tiles').append(h('div',{class:'tile'},h('div',{class:'k',text:k}),h('div',{class:'v',text:v})));
const g={};R.forEach(r=>{const k=r.leg+'|'+r.kind;(g[k]=g[k]||{n:0,p:0,w:0});g[k].n++;g[k].p+=r.pnl;g[k].w+=r.pnl>0;});
const lt=$('legs');lt.append(h('tr',{},h('th',{text:'腿'}),h('th',{text:'离场'}),h('th',{class:'n',text:'笔数'}),h('th',{class:'n',text:'胜率'}),h('th',{class:'n',text:'盈亏 U'})));
Object.entries(g).sort().forEach(([k,v])=>{const [a,b]=k.split('|');lt.append(h('tr',{},h('td',{text:a}),h('td',{text:KIND[b]||b}),h('td',{class:'n',text:f(v.n)}),h('td',{class:'n',text:f(v.w/v.n*100)+'%'}),h('td',{class:'n '+(v.p>=0?'pos':'neg'),text:sg(v.p)})));});
[...new Set(R.map(r=>r.leg))].forEach(x=>$('f-leg').append(h('option',{value:x,text:x})));
[...new Set(R.map(r=>r.kind))].forEach(x=>$('f-kind').append(h('option',{value:x,text:KIND[x]||x})));
const cols=[['#','i',1],['币','inst'],['方向','side'],['腿','leg'],['开仓','tin'],['平仓','tout'],['持有','hold',1],['开仓价','entry',1],['平仓价','exit',1],['止损距离','stopd',1],['名义 U','notional',1],['保证金 U','margin',1],['盈亏 U','pnl',1],['R','R',1],['离场','kind']];
let key='tin',asc=true;
function draw(){
  const L=$('f-leg').value,K=$('f-kind').value,W=$('f-res').value,I=$('f-inst').value.trim().toUpperCase();
  let rows=R.map((r,i)=>Object.assign({i:i+1,hold:(r.tout-r.tin)/3600e3},r)).filter(r=>(!L||r.leg===L)&&(!K||r.kind===K)&&(!W||(W==='w'?r.pnl>0:r.pnl<=0))&&(!I||r.inst.includes(I)));
  rows.sort((a,b)=>{const x=a[key],y=b[key];return (x<y?-1:x>y?1:0)*(asc?1:-1);});
  const t=$('t');t.textContent='';const hr=h('tr');
  cols.forEach(([n,k,num])=>{const th=h('th',{class:num?'n':'',text:n+(k===key?(asc?' ↑':' ↓'):'')});th.onclick=()=>{if(key===k)asc=!asc;else{key=k;asc=k==='tin'||k==='inst';}draw();};hr.append(th);});
  t.append(h('thead',{},hr));const tb=h('tbody');
  rows.forEach(r=>{
    const tr=h('tr',{class:'row'});
    const v={i:r.i,inst:r.inst,side:r.side==='long'?'多':'空',leg:r.leg,tin:tm(r.tin),tout:tm(r.tout),hold:f(r.hold)+'h',entry:f(r.entry,6),exit:f(r.exit,6),stopd:f(r.stopd*100,1)+'%',notional:f(r.notional),margin:f(r.margin),pnl:sg(r.pnl),R:f(r.R,2),kind:KIND[r.kind]||r.kind};
    cols.forEach(([n,k,num])=>tr.append(h('td',{class:(num?'n ':'')+(k==='pnl'||k==='R'?(r.pnl>=0?'pos':'neg'):''),text:String(v[k])})));
    const det=h('tr',{class:'det'});det.hidden=true;det.append(h('td',{colspan:String(cols.length),text:`开仓理由：${r.reason} · 止损价 ${f(r.stop,6)} · 杠杆 ${f(r.lever,2)}x · 手续费 ${f(r.fees,2)} U · 资金费 ${f(r.funding,2)} U`}));
    tr.onclick=()=>{det.hidden=!det.hidden;};tb.append(tr,det);
  });
  t.append(tb);$('count').textContent=`${rows.length} 笔 · 合计 ${sg(rows.reduce((p,r)=>p+r.pnl,0))} U`;
}
['f-leg','f-kind','f-res'].forEach(id=>$(id).onchange=draw);$('f-inst').oninput=draw;draw();
})();
</script></body></html>"""

if __name__ == "__main__":
    main()
