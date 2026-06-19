const $ = selector => document.querySelector(selector);
const state = {register: false, offers: [], services: [], orders: [], selectedService: '', liveSmsPurchaseEnabled: false};

async function api(path, options = {}) {
  const response = await fetch(path, {headers: {'content-type': 'application/json'}, ...options});
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `请求失败 ${response.status}`);
  return data;
}

function money(fen) { return `¥${(Number(fen) / 100).toFixed(2)}`; }
function toast(message) {
  const element = $('#toast');
  element.textContent = message;
  element.classList.add('show');
  setTimeout(() => element.classList.remove('show'), 2600);
}
function escapeHTML(value) {
  return String(value ?? '').replace(/[&<>'"]/g, char => ({'&': '&amp;', '<': '&lt;', '>': '&gt;', "'": '&#39;', '"': '&quot;'}[char]));
}

function serviceIcon(code, name = '') {
  const key = `${code} ${name}`.toLowerCase();
  if (code === 'tg' || key.includes('telegram')) return '<span class="service-icon telegram" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M20.7 4.2 3.8 10.7c-1.2.5-1.2 1.1-.2 1.4l4.3 1.4 1.7 5.1c.2.7.1 1 .8 1 .5 0 .8-.2 1-.4l2.1-2 4.4 3.2c.8.5 1.4.2 1.6-.8l2.9-13.8c.3-1.3-.5-1.9-1.7-1.6ZM9 13.2l9.7-6.1c.5-.3.9-.1.5.2l-8 7.2-.3 3.2L9 13.2Z"/></svg></span>';
  if (code === 'wa' || key.includes('whatsapp')) return '<span class="service-icon whatsapp" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M12 3a8.5 8.5 0 0 0-7.4 12.7L3.5 20.5l4.8-1.1A8.5 8.5 0 1 0 12 3Zm4.9 12.3c-.2.6-1.2 1.1-1.8 1.2-.5.1-1.2.2-3.8-.9-3.2-1.4-5.2-4.6-5.4-4.8-.2-.2-1.3-1.7-1.3-3.2s.8-2.3 1.1-2.6c.3-.3.7-.4 1-.4h.7c.2 0 .5-.1.8.6l1 2.4c.1.2.1.5 0 .7l-.4.7c-.2.2-.4.5-.2.8.2.3.8 1.3 1.8 2.1 1.2 1.1 2.3 1.5 2.6 1.7.3.2.5.1.7-.1l1.1-1.3c.2-.3.5-.3.8-.2l2.2 1c.3.2.6.2.7.4.1.1.1.8-.1 1.4Z"/></svg></span>';
  if (code === 'ig' || key.includes('instagram')) return '<span class="service-icon instagram" aria-hidden="true"><svg viewBox="0 0 24 24"><rect x="4" y="4" width="16" height="16" rx="5"/><circle cx="12" cy="12" r="4"/><circle cx="17.5" cy="6.8" r="1"/></svg></span>';
  if (key.includes('gmx')) return '<span class="service-icon gmx" aria-hidden="true"><svg viewBox="0 0 24 24"><path d="M3.904 11.571v1.501H5.46c-.075.845-.712 1.274-1.539 1.274-1.255 0-1.934-1.157-1.934-2.3 0-1.118.65-2.317 1.906-2.317.77 0 1.321.468 1.586 1.166l1.812-.76C6.66 8.765 5.489 8.086 3.979 8.086 1.614 8.087 0 9.654 0 12.037c0 2.309 1.604 3.876 3.913 3.876 1.227 0 2.308-.439 3.025-1.44.651-.916.731-1.831.75-2.904zM13.65 8.3l-1.586 3.95-1.5-3.95H8.67l-1.255 7.392h1.91l.619-4.257h.019l1.695 4.257h.765l1.775-4.257h.024l.538 4.257h1.92L15.562 8.3zm7.708 3.473 2.086-3.475h-2.128l-1.11 1.767L19.012 8.3H16.68l2.459 3.47-2.46 3.922h2.333l1.33-2.223 1.576 2.223H24l-2.642-3.92"/></svg></span>';
  const brand = serviceBrand(key);
  const icon = brand && globalThis.SERVICE_ICONS?.[brand];
  if (icon) {
    return `<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 ${icon.width} ${icon.height}">${icon.body}</svg></span>`;
  }
  return generatedServiceIcon(code, name);
}

const serviceBrands = [
  ['gmail', 'google-gmail'], ['google', 'google'], ['facebook', 'facebook'], ['discord', 'discord-icon'],
  ['tiktok', 'tiktok-icon'], ['linkedin', 'linkedin-icon'], ['pinterest', 'pinterest'],
  ['reddit', 'reddit-icon'], ['twitch', 'twitch'], ['steam', 'steam'], ['github', 'github-icon'],
  ['gitlab', 'gitlab-icon'], ['microsoft', 'microsoft-icon'], ['apple', 'apple'], ['yahoo', 'yahoo'],
  ['paypal', 'paypal'], ['netflix', 'netflix-icon'], ['spotify', 'spotify-icon'], ['airbnb', 'airbnb-icon'],
  ['dropbox', 'dropbox'], ['cloudflare', 'cloudflare-icon'], ['openai', 'openai-icon'], ['chatgpt', 'openai-icon'],
  ['signal', 'signal'], ['slack', 'slack-icon'], ['notion', 'notion-icon'], ['zoom', 'zoom-icon']
];

function serviceBrand(key) {
  const code = key.split(' ', 1)[0];
  const codeBrand = {go: 'google', fb: 'facebook', ds: 'discord-icon'}[code];
  if (codeBrand) return codeBrand;
  const match = serviceBrands.find(([needle]) => key.includes(needle));
  return match ? match[1] : null;
}

function generatedServiceIcon(code, name = '') {
  const label = (name || code || '?').trim();
  const letters = (label.match(/[a-z0-9]/gi) || ['?']).slice(0, 2).join('').toUpperCase();
  let hash = 0;
  for (const char of `${code}:${name}`) hash = ((hash * 31) + char.charCodeAt(0)) >>> 0;
  const hue = hash % 360;
  const hue2 = (hue + 42 + (hash % 50)) % 360;
  return `<span class="service-icon generated" aria-hidden="true"><svg viewBox="0 0 34 34"><defs><linearGradient id="service-${hash}" x1="0" y1="0" x2="1" y2="1"><stop stop-color="hsl(${hue} 68% 52%)"/><stop offset="1" stop-color="hsl(${hue2} 72% 42%)"/></linearGradient></defs><rect width="34" height="34" rx="10" fill="url(#service-${hash})"/><circle cx="27" cy="7" r="4" fill="white" opacity=".18"/><text x="17" y="21.5" text-anchor="middle">${escapeHTML(letters)}</text></svg></span>`;
}

async function boot() {
  try {
    const me = await api('/api/me');
    showApp(me);
    await Promise.all([loadCatalog(), loadOrders()]);
    const orderID = new URLSearchParams(location.search).get('order');
    if (orderID) {
      toast('付款已返回，正在确认支付并取号');
      pollOrder(orderID);
    }
  } catch {
    $('#auth').classList.remove('hidden');
  }
}

function showApp(data) {
  state.liveSmsPurchaseEnabled = Boolean(data.liveSmsPurchaseEnabled);
  $('#auth').classList.add('hidden');
  $('#app').classList.remove('hidden');
  $('#logout').classList.remove('hidden');
  if (!state.liveSmsPurchaseEnabled) {
    $('#buy').textContent = '支付取号演示模式';
    $('#stock').textContent = '沙箱配置禁止消耗真实 HeroSMS 余额';
  }
}

$('#toggleAuth').onclick = () => {
  state.register = !state.register;
  $('#authTitle').textContent = state.register ? '创建账户' : '登录账户';
  $('#toggleAuth').textContent = state.register ? '已有账户？返回登录' : '没有账户？立即注册';
};
$('#authForm').onsubmit = async event => {
  event.preventDefault();
  $('#authError').textContent = '';
  try {
    await api(`/api/auth/${state.register ? 'register' : 'login'}`, {method: 'POST', body: JSON.stringify({Email: $('#email').value, Password: $('#password').value})});
    showApp(await api('/api/me'));
    await Promise.all([loadCatalog(), loadOrders()]);
  } catch (error) {
    $('#authError').textContent = error.message;
  }
};
$('#logout').onclick = async () => { await api('/api/auth/logout', {method: 'POST'}); location.reload(); };

async function loadCatalog() {
  const data = await api('/api/catalog');
  $('#country').innerHTML = '<option value="">选择国家</option>' + data.countries.filter(country => country.visible !== 0).map(country => `<option value="${country.id}">${escapeHTML(country.chn || country.eng)} · ${escapeHTML(country.eng)}</option>`).join('');
}
$('#country').onchange = async () => {
  if (!$('#country').value) return;
  state.selectedService = '';
  $('#service').innerHTML = '<div class="service-loading">载入中...</div>';
  const data = await api(`/api/catalog?country=${encodeURIComponent($('#country').value)}`);
  state.services = data.services;
  state.offers = data.offers;
  renderServices();
};
$('#search').oninput = renderServices;

function renderServices() {
  const query = $('#search').value.toLowerCase();
  const available = new Map(state.offers.map(offer => [offer.service, offer]));
  const services = state.services.filter(service => available.has(service.code) && (`${service.name} ${service.code}`).toLowerCase().includes(query));
  $('#service').innerHTML = services.length ? services.map(service => `<button type="button" class="service-option${state.selectedService === service.code ? ' selected' : ''}" data-service="${escapeHTML(service.code)}" role="option" aria-selected="${state.selectedService === service.code}">${serviceIcon(service.code, service.name)}<span><b>${escapeHTML(service.name)}</b><small>${escapeHTML(service.code)}</small></span></button>`).join('') : '<div class="service-loading">没有可用服务</div>';
  document.querySelectorAll('.service-option').forEach(button => button.onclick = () => selectService(button.dataset.service));
  selectOffer();
}
function selectService(code) {
  state.selectedService = code;
  renderServices();
}
function selectOffer() {
  const offer = state.offers.find(item => item.service === state.selectedService);
  $('#buy').disabled = !offer || !state.liveSmsPurchaseEnabled;
  $('#price').textContent = offer ? money(offer.priceFen) : '请选择服务';
  $('#stock').textContent = offer ? (state.liveSmsPurchaseEnabled ? `实时库存 ${offer.count} 个 · 已含 ¥1 服务费` : `实时库存 ${offer.count} 个 · 沙箱禁止真实取号`) : '';
}

$('#buy').onclick = async () => {
  const button = $('#buy');
  button.disabled = true;
  button.textContent = '正在创建支付订单...';
  try {
    const order = await api('/api/orders', {method: 'POST', body: JSON.stringify({Country: $('#country').value, Service: state.selectedService, payType: Number($('#payType').value)})});
    toast(`本单应付 ${money(order.priceFen)}，正在前往支付`);
    location.href = order.checkoutUrl;
  } catch (error) {
    toast(error.message);
    button.textContent = state.liveSmsPurchaseEnabled ? '支付并取号' : '支付取号演示模式';
    selectOffer();
  }
};

async function loadOrders() { state.orders = await api('/api/orders'); renderOrders(); }
function renderOrders() {
  const box = $('#orders');
  if (!state.orders.length) {
    box.innerHTML = '<div class="empty">还没有订单。选一个服务开始吧。</div>';
    return;
  }
  box.innerHTML = state.orders.map(order => `<article class="order"><div class="order-service">${serviceIcon(order.Service)}<span><b>${escapeHTML(order.Phone || order.Service)}</b><small>国家 ${escapeHTML(order.Country)}${order.ReplaceAttempts ? ` · 已换号 ${order.ReplaceAttempts} 次` : ''}</small></span></div><div><span class="badge">${status(order.Status)}</span><small>${new Date(order.CreatedAt).toLocaleString()}</small></div><div>${order.Code ? `<code>${escapeHTML(order.Code)}</code>` : '<small>等待验证码</small>'}<b>${money(order.PriceFen)}</b></div><div><button class="link refresh-one" data-id="${order.ID}">刷新</button></div></article>`).join('');
  document.querySelectorAll('.refresh-one').forEach(button => button.onclick = () => refreshOrder(button.dataset.id));
}
function status(value) {
  return ({awaiting_payment: '等待支付', payment_failed: '支付创建失败', paid: '支付成功，等待取号', purchasing: '正在取号', waiting: '等待短信', replacing: '正在取消并换号', code_received: '已收到', cancelled: '已取消', purchase_failed: '历史取号失败', replace_failed: '历史换号失败', finished: '已完成'})[value] || value;
}
async function refreshOrder(id) {
  try { await api(`/api/orders/${id}`); await loadOrders(); } catch (error) { toast(error.message); }
}
async function pollOrder(id) {
  for (let attempt = 0; attempt < 60; attempt++) {
    try {
      const order = await api(`/api/orders/${id}`);
      await loadOrders();
      if (order.Status === 'waiting' || order.Status === 'replacing' || order.Status === 'code_received') {
        toast(order.Status === 'code_received' ? '验证码已收到' : '支付已确认，号码已分配');
        history.replaceState({}, '', location.pathname);
        return;
      }
      if (order.Status === 'payment_failed') {
        toast('支付订单创建失败');
        return;
      }
    } catch {}
    await new Promise(resolve => setTimeout(resolve, 2000));
  }
  toast('支付或取号仍在处理中，请在订单列表继续查看');
}
$('#refresh').onclick = loadOrders;
setInterval(() => {
  if (!$('#app').classList.contains('hidden') && state.orders.some(order => ['paid', 'purchasing', 'waiting', 'replacing'].includes(order.Status))) loadOrders().catch(() => {});
}, 5000);

boot();
