const $ = selector => document.querySelector(selector);
const adminState = {view: 'dashboard', userId: 0, threads: []};

async function request(path, options = {}) {
  const response = await fetch(path, {headers: {'content-type': 'application/json'}, ...options});
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `请求失败 ${response.status}`);
  return data;
}
function esc(value) { return String(value ?? '').replace(/[&<>'"]/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[char])); }
function money(fen) { return `¥${(Number(fen || 0) / 100).toFixed(2)}`; }
function toast(text) { const box=$('#toast'); box.textContent=text; box.classList.add('show'); setTimeout(()=>box.classList.remove('show'),2200); }
function date(value) { return value ? new Date(value).toLocaleString('zh-CN') : '-'; }

$('#loginForm').onsubmit = async event => {
  event.preventDefault(); $('#loginError').textContent='';
  try { await request('/api/admin/login',{method:'POST',body:JSON.stringify({Email:$('#email').value,Password:$('#password').value})}); showPanel(); }
  catch (error) { $('#loginError').textContent=error.message; }
};
$('#logout').onclick = async () => { await request('/api/admin/logout',{method:'POST'}); location.reload(); };
$('#refresh').onclick = () => loadCurrent().catch(error=>toast(error.message));
document.querySelectorAll('nav [data-view]').forEach(button => button.onclick=()=>switchView(button.dataset.view));
function switchView(view) {
  adminState.view=view; document.querySelectorAll('.view').forEach(x=>x.classList.toggle('hidden',x.id!==view)); document.querySelectorAll('nav [data-view]').forEach(x=>x.classList.toggle('active',x.dataset.view===view));
  $('#viewTitle').textContent=({dashboard:'数据概览',support:'客服聊天',settings:'联系方式'})[view]; loadCurrent().catch(error=>toast(error.message));
}
function showPanel() { $('#login').classList.add('hidden'); $('#panel').classList.remove('hidden'); loadOverview().catch(error=>{if(error.message.includes('admin login')) location.reload(); else toast(error.message)}); loadThreads().catch(()=>{}); }
function loadCurrent(){ return adminState.view==='dashboard'?loadOverview():adminState.view==='support'?loadThreads():loadSettings(); }
async function loadOverview(){
  const data=await request('/api/admin/overview'), s=data.stats;
  $('#ordersTotal').textContent=s.ordersTotal; $('#ordersToday').textContent=s.ordersToday; $('#ordersSuccessful').textContent=s.ordersSuccessful; $('#revenue').textContent=money(s.revenueFen); $('#usersTotal').textContent=s.usersTotal;
  renderBalance('hero',data.balances.hero); renderBalance('smsman',data.balances.smsman);
  $('#orders').innerHTML=data.orders.length?data.orders.map(o=>`<tr><td>${esc(o.id.slice(0,10))}</td><td>${esc(o.email)}</td><td>${esc(o.country)} / ${esc(o.service)}</td><td>${esc(o.provider)}</td><td>${money(o.priceFen)}</td><td><span class="status">${esc(o.status)}</span></td><td>${date(o.createdAt)}</td></tr>`).join(''):'<tr><td colspan="7">暂无订单</td></tr>';
}
function renderBalance(id,item){ const value=$(`#${id}Balance`), error=$(`#${id}Error`); if(!item){value.textContent='未配置';error.textContent='';return} value.textContent=item.available?`${Number(item.amount).toFixed(2)} ${item.currency}`:'读取失败'; error.textContent=item.error||''; }
async function loadThreads(){
  adminState.threads=await request('/api/admin/chats'); const unread=adminState.threads.reduce((sum,x)=>sum+x.unread,0); $('#chatCount').textContent=unread||'';
  $('#threads').innerHTML=adminState.threads.length?adminState.threads.map(t=>`<button class="thread${t.userId===adminState.userId?' active':''}" data-user="${t.userId}">${t.unread?`<i>${t.unread}</i>`:''}<b>${esc(t.email)}</b><span>${esc(t.lastMessage)}</span><span>${date(t.lastAt)}</span></button>`).join(''):'<div style="padding:22px;color:#8491a5">暂无客服会话</div>';
  document.querySelectorAll('.thread').forEach(button=>button.onclick=()=>openChat(Number(button.dataset.user)));
  if(adminState.userId) await loadMessages();
}
async function openChat(userId){ adminState.userId=userId; const thread=adminState.threads.find(x=>x.userId===userId); $('#chatHeader').textContent=thread?.email||`用户 ${userId}`; $('#reply').disabled=false; $('#replyForm button').disabled=false; await loadMessages(); await loadThreads(); }
async function loadMessages(){ if(!adminState.userId)return; const messages=await request(`/api/admin/chats/${adminState.userId}`); const box=$('#messages'); box.innerHTML=messages.map(m=>`<div class="message ${m.sender}">${esc(m.body)}<small>${date(m.createdAt)}</small></div>`).join(''); box.scrollTop=box.scrollHeight; }
$('#replyForm').onsubmit=async event=>{event.preventDefault();const body=$('#reply').value.trim();if(!body)return;await request(`/api/admin/chats/${adminState.userId}`,{method:'POST',body:JSON.stringify({body})});$('#reply').value='';await loadMessages();};
async function loadSettings(){const data=await request('/api/admin/settings');Object.keys(data).forEach(key=>{const input=$(`#${key}`);if(input)input.value=data[key]});}
$('#settingsForm').onsubmit=async event=>{event.preventDefault();const body={contactTitle:$('#contactTitle').value,contactValue:$('#contactValue').value,contactURL:$('#contactURL').value,supportHours:$('#supportHours').value};await request('/api/admin/settings',{method:'PUT',body:JSON.stringify(body)});$('#settingsResult').textContent='已保存';setTimeout(()=>$('#settingsResult').textContent='',1800);};
setInterval(()=>{if(!$('#panel').classList.contains('hidden')){loadThreads().catch(()=>{});if(adminState.view==='dashboard')loadOverview().catch(()=>{});}},10000);
request('/api/admin/overview').then(showPanel).catch(()=>{});
