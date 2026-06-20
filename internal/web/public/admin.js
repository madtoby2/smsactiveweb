const $ = selector => document.querySelector(selector);
const adminState = {view: 'dashboard', userId: 0, threads: [], announcements: []};

async function request(path, options = {}) {
  const response = await fetch(path, {headers: {'content-type': 'application/json'}, ...options});
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `请求失败 ${response.status}`);
  return data;
}
function esc(value) { return String(value ?? '').replace(/[&<>'"]/g, char => ({'&':'&amp;','<':'&lt;','>':'&gt;',"'":'&#39;','"':'&quot;'}[char])); }
function money(fen) { return `¥${(Number(fen || 0) / 100).toFixed(2)}`; }
function date(value) { return value ? new Date(value).toLocaleString('zh-CN') : '-'; }
function toast(text) { const box=$('#toast'); box.textContent=text; box.classList.add('show'); setTimeout(()=>box.classList.remove('show'),2200); }
function status(value) { return `<span class="status">${esc(value)}</span>`; }

$('#loginForm').onsubmit = async event => {
  event.preventDefault(); $('#loginError').textContent='';
  try { await request('/api/admin/login',{method:'POST',body:JSON.stringify({Email:$('#email').value,Password:$('#password').value})}); showPanel(); }
  catch (error) { $('#loginError').textContent=error.message; }
};
$('#logout').onclick = async () => { await request('/api/admin/logout',{method:'POST'}); location.reload(); };
$('#refresh').onclick = () => loadCurrent().catch(error=>toast(error.message));

const titles={dashboard:'数据概览',ordersView:'订单管理',payments:'支付流水',users:'用户管理',announcements:'公告管理',support:'客服聊天',audit:'操作审计',settings:'系统设置'};
document.querySelectorAll('nav [data-view]').forEach(button => button.onclick=()=>switchView(button.dataset.view));
function switchView(view) {
  adminState.view=view;
  document.querySelectorAll('.view').forEach(element=>element.classList.toggle('hidden',element.id!==view));
  document.querySelectorAll('nav [data-view]').forEach(element=>element.classList.toggle('active',element.dataset.view===view));
  $('#viewTitle').textContent=titles[view]; loadCurrent().catch(error=>toast(error.message));
}
function showPanel() { $('#login').classList.add('hidden'); $('#panel').classList.remove('hidden'); loadOverview().catch(error=>{if(error.message.includes('admin login')) location.reload(); else toast(error.message)}); loadThreads().catch(()=>{}); }
function loadCurrent(){ return ({dashboard:loadOverview,ordersView:loadOrders,payments:loadPayments,users:loadUsers,announcements:loadAnnouncements,support:loadThreads,audit:loadAudit,settings:loadSettings})[adminState.view](); }

function orderRows(orders) {
  return orders.length?orders.map(order=>`<tr><td>${esc(order.id.slice(0,12))}</td><td>${esc(order.email)}</td><td>${esc(order.country)} / ${esc(order.service)}</td><td>${esc(order.provider)}</td><td>${money(order.priceFen)}</td><td>${status(order.status)}</td><td>${date(order.createdAt)}</td></tr>`).join(''):'<tr><td colspan="7" class="muted">暂无订单</td></tr>';
}
async function loadOverview(){
  const data=await request('/api/admin/overview'), stats=data.stats;
  $('#ordersTotal').textContent=stats.ordersTotal; $('#ordersToday').textContent=stats.ordersToday; $('#ordersSuccessful').textContent=stats.ordersSuccessful; $('#revenue').textContent=money(stats.revenueFen); $('#usersTotal').textContent=stats.usersTotal;
  renderBalance('hero',data.balances.hero); renderBalance('smsman',data.balances.smsman); $('#recentOrders').innerHTML=orderRows(data.orders);
}
function renderBalance(id,item){ const value=$(`#${id}Balance`), error=$(`#${id}Error`); if(!item){value.textContent='未配置';error.textContent='';return} value.textContent=item.available?`${Number(item.amount).toFixed(2)} ${item.currency}`:'读取失败'; error.textContent=item.error||''; }

async function loadOrders(){ const query=new URLSearchParams({q:$('#orderQuery').value,status:$('#orderStatus').value}); $('#allOrders').innerHTML=orderRows(await request(`/api/admin/orders?${query}`)); }
$('#searchOrders').onclick=loadOrders;
async function loadPayments(){
  const query=new URLSearchParams({q:$('#paymentQuery').value,status:$('#paymentStatus').value}), items=await request(`/api/admin/payments?${query}`);
  $('#paymentRows').innerHTML=items.length?items.map(item=>`<tr><td>${esc(item.id.slice(0,12))}</td><td>${esc(item.email)}</td><td>${esc(item.orderId.slice(0,12))}</td><td>${esc(item.provider)} / ${esc(item.payType)}</td><td>${money(item.amountFen)}</td><td>${status(item.status)}</td><td>${date(item.createdAt)}</td></tr>`).join(''):'<tr><td colspan="7" class="muted">暂无支付记录</td></tr>';
}
$('#searchPayments').onclick=loadPayments;
async function loadUsers(){
  const items=await request(`/api/admin/users?q=${encodeURIComponent($('#userQuery').value)}`);
  $('#userRows').innerHTML=items.length?items.map(user=>`<tr><td>${user.id}</td><td>${esc(user.email)}</td><td>${user.orders}</td><td>${money(user.spentFen)}</td><td>${money(user.balanceFen)}</td><td>${date(user.createdAt)}</td><td>${user.disabled?'<span class="status">已封禁</span>':'正常'}</td><td><button class="action ${user.disabled?'':'danger'} user-toggle" data-id="${user.id}" data-disabled="${!user.disabled}">${user.disabled?'解封':'封禁'}</button></td></tr>`).join(''):'<tr><td colspan="8" class="muted">暂无用户</td></tr>';
  document.querySelectorAll('.user-toggle').forEach(button=>button.onclick=()=>toggleUser(button));
}
$('#searchUsers').onclick=loadUsers;
async function toggleUser(button){ const disabled=button.dataset.disabled==='true'; if(!confirm(`确认${disabled?'封禁':'解封'}该用户？`))return; await request(`/api/admin/users/${button.dataset.id}`,{method:'PATCH',body:JSON.stringify({disabled})}); toast('用户状态已更新'); await loadUsers(); }

async function loadAnnouncements(){
  adminState.announcements=await request('/api/admin/announcements');
  $('#announcementList').innerHTML=adminState.announcements.length?adminState.announcements.map(item=>`<section class="announcement-item"><header><b>${esc(item.title)}</b>${item.active?'':' <span class="status">已停用</span>'}</header><p>${esc(item.body)}</p><small>${date(item.updatedAt)}</small><footer><button class="action ann-edit" data-id="${item.id}">编辑</button><button class="action danger ann-delete" data-id="${item.id}">删除</button></footer></section>`).join(''):'<p class="muted">暂无公告</p>';
  document.querySelectorAll('.ann-edit').forEach(button=>button.onclick=()=>editAnnouncement(Number(button.dataset.id)));
  document.querySelectorAll('.ann-delete').forEach(button=>button.onclick=()=>deleteAnnouncement(Number(button.dataset.id)));
}
function editAnnouncement(id){ const item=adminState.announcements.find(value=>value.id===id); if(!item)return; $('#announcementID').value=item.id; $('#announcementTitle').value=item.title; $('#announcementBody').value=item.body; $('#announcementActive').checked=item.active; $('#announcementFormTitle').textContent='编辑公告'; }
function resetAnnouncement(){ $('#announcementForm').reset(); $('#announcementID').value=''; $('#announcementActive').checked=true; $('#announcementFormTitle').textContent='发布公告'; }
$('#cancelAnnouncement').onclick=resetAnnouncement;
$('#announcementForm').onsubmit=async event=>{event.preventDefault();const id=$('#announcementID').value;const body={title:$('#announcementTitle').value,body:$('#announcementBody').value,active:$('#announcementActive').checked};await request(id?`/api/admin/announcements/${id}`:'/api/admin/announcements',{method:id?'PUT':'POST',body:JSON.stringify(body)});resetAnnouncement();toast('公告已保存');await loadAnnouncements();};
async function deleteAnnouncement(id){if(!confirm('确认删除这条公告？'))return;await request(`/api/admin/announcements/${id}`,{method:'DELETE'});toast('公告已删除');await loadAnnouncements();}

async function loadThreads(){
  adminState.threads=await request('/api/admin/chats'); const unread=adminState.threads.reduce((sum,item)=>sum+item.unread,0); $('#chatCount').textContent=unread||'';
  $('#threads').innerHTML=adminState.threads.length?adminState.threads.map(thread=>`<button class="thread${thread.userId===adminState.userId?' active':''}" data-user="${thread.userId}">${thread.unread?`<i>${thread.unread}</i>`:''}<b>${esc(thread.email)}</b><span>${esc(thread.lastMessage)}</span><span>${date(thread.lastAt)}</span></button>`).join(''):'<div class="muted" style="padding:22px">暂无客服会话</div>';
  document.querySelectorAll('.thread').forEach(button=>button.onclick=()=>openChat(Number(button.dataset.user))); if(adminState.userId)await loadMessages();
}
async function openChat(userId){adminState.userId=userId;const thread=adminState.threads.find(item=>item.userId===userId);$('#chatHeader').textContent=thread?.email||`用户 ${userId}`;$('#reply').disabled=false;$('#replyForm button').disabled=false;await loadMessages();await loadThreads();}
async function loadMessages(){if(!adminState.userId)return;const messages=await request(`/api/admin/chats/${adminState.userId}`),box=$('#messages');box.innerHTML=messages.map(message=>`<div class="message ${message.sender}">${esc(message.body)}<small>${date(message.createdAt)}</small></div>`).join('');box.scrollTop=box.scrollHeight;}
$('#replyForm').onsubmit=async event=>{event.preventDefault();const body=$('#reply').value.trim();if(!body)return;await request(`/api/admin/chats/${adminState.userId}`,{method:'POST',body:JSON.stringify({body})});$('#reply').value='';await loadMessages();};

async function loadAudit(){const items=await request('/api/admin/audit');$('#auditRows').innerHTML=items.length?items.map(item=>`<tr><td>${esc(item.action)}</td><td>${esc(item.target)}</td><td>${esc(item.detail)}</td><td>${date(item.at)}</td></tr>`).join(''):'<tr><td colspan="4" class="muted">暂无操作记录</td></tr>';}
async function loadSettings(){const data=await request('/api/admin/settings');Object.keys(data).forEach(key=>{const input=$(`#${key}`);if(input)input.value=data[key]});}
$('#settingsForm').onsubmit=async event=>{event.preventDefault();const keys=['markupCNY','usdCnyRate','smsmanCnyRate','contactTitle','contactValue','contactURL','supportHours'],body={};keys.forEach(key=>body[key]=$(`#${key}`).value);await request('/api/admin/settings',{method:'PUT',body:JSON.stringify(body)});$('#settingsResult').textContent='已保存并生效';setTimeout(()=>$('#settingsResult').textContent='',1800);};

setInterval(()=>{if(!$('#panel').classList.contains('hidden')){loadThreads().catch(()=>{});if(adminState.view==='dashboard')loadOverview().catch(()=>{});}},10000);
request('/api/admin/overview').then(showPanel).catch(()=>{});
