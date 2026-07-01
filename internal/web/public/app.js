const $ = selector => document.querySelector(selector);
const state = {
  register: false,
  user: null,
  offers: [],
  services: [],
  allServices: [],
  countries: [],
  orders: [],
  selectedService: '',
  selectedCountry: '',
  liveSmsPurchaseEnabled: false,
  authConfig: {emailVerificationRequired: false, emailVerificationAvailable: false, turnstileSiteKey: ''},
  turnstileWidget: null,
  previewHydrated: false,
  globalCatalog: {countries: [], services: [], offers: []},
  countryCatalog: null,
  countryCatalogLoading: false,
  countryRequestSerial: 0,
};
const COOKIE_CONSENT_KEY = 'cookieConsentChoice';
const CATALOG_CACHE_KEY = 'catalogCacheV2';
const CATALOG_CACHE_TTL = 5 * 60 * 1000;

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
const COUNTRY_FALLBACKS = {
  '1': '俄罗斯',
  '2': '乌克兰',
  '3': '哈萨克斯坦',
  '4': '菲律宾',
  '5': '缅甸',
  '6': '印度尼西亚',
  '7': '马来西亚',
  '8': '越南',
  '9': '泰国',
  '10': '中国香港',
  '11': '日本',
  '12': '美国',
};
function countryName(value) {
  const key = String(value ?? '').trim();
  if (!key) return '';
  const match = state.countries.find(country => String(country.id) === key);
  return match ? (match.chn || match.eng || key) : (COUNTRY_FALLBACKS[key] || `国家 ${key}`);
}
function orderCountryName(order) {
  const saved = String(order?.CountryName ?? '').trim();
  if (saved) return saved;
  const key = String(order?.Country ?? '').trim();
  const match = state.countries.find(country => String(country.id) === key);
  if (match) return match.chn || match.eng || key;
  return COUNTRY_FALLBACKS[key] || `国家 ${key}`;
}
function replaceAttemptsText(value) {
  const count = Number(value || 0);
  if (!count) return '';
  return ` · 已换号 ${Math.min(count, 20)} 次`;
}
function formatPhoneNumber(value, country) {
  const raw = String(value ?? '').trim();
  const digits = raw.replace(/\D/g, '');
  const countryID = String(country ?? '').trim();
  if ((countryID === '4' || raw.startsWith('63')) && digits.length >= 12 && digits.startsWith('63')) {
    return `+63 (${digits.slice(2, 5)}) ${digits.slice(5)}`;
  }
  return raw;
}
function splitPhoneNumber(value, country) {
  const formatted = formatPhoneNumber(value, country);
  const match = formatted.match(/^(\+\d+(?:\s*\(\d+\))?)(?:\s+)(.+)$/);
  if (match) return {prefix: match[1], number: match[2]};
  const digits = String(value ?? '').replace(/\D/g, '');
  if (digits.length > 4) {
    return {prefix: digits.slice(0, Math.min(4, digits.length - 4)), number: digits.slice(Math.min(4, digits.length - 4))};
  }
  return {prefix: '', number: formatted};
}
function serviceDisplayName(code) {
  const key = String(code ?? '').trim();
  const match = state.services.find(service => String(service.code) === key);
  if (match?.name) return match.name;
  return ({mb: 'Yahoo', ub: 'Uber', dr: 'OpenAI'})[key] || key;
}
function hasMeaningfulCatalogData(data) {
  if (!data) return false;
  return Array.isArray(data.offers) && data.offers.length > 0;
}
function countryFlagCode(value, country) {
  const key = String(value ?? '').trim();
  const match = state.countries.find(country => String(country.id) === key);
  const name = String(country?.eng || country?.chn || match?.eng || match?.chn || COUNTRY_FALLBACKS[key] || '').trim();
  const map = {
    Ukraine: 'UA',
    Kazakhstan: 'KZ',
    China: 'CN',
    Philippines: 'PH',
    Myanmar: 'MM',
    Indonesia: 'ID',
    Malaysia: 'MY',
    Kenya: 'KE',
    Tanzania: 'TZ',
    Vietnam: 'VN',
    Kyrgyzstan: 'KG',
    Israel: 'IL',
    'Hong Kong': 'HK',
    Poland: 'PL',
    'United Kingdom': 'GB',
    Madagascar: 'MG',
    'DR Congo': 'CD',
    Nigeria: 'NG',
    Macao: 'MO',
    Egypt: 'EG',
    India: 'IN',
    Ireland: 'IE',
    Cambodia: 'KH',
    Laos: 'LA',
    Haiti: 'HT',
    Serbia: 'RS',
    Yemen: 'YE',
    'South Africa': 'ZA',
    Romania: 'RO',
    Colombia: 'CO',
    Estonia: 'EE',
    Azerbaijan: 'AZ',
    Canada: 'CA',
    Morocco: 'MA',
    Ghana: 'GH',
    Argentina: 'AR',
    Uzbekistan: 'UZ',
    Germany: 'DE',
    Japan: 'JP',
    Thailand: 'TH',
    'United States': 'US',
    Australia: 'AU',
    Brazil: 'BR',
    France: 'FR',
    Singapore: 'SG',
    Turkey: 'TR',
  };
  const code = map[name] || map[countryName(value)] || '';
  return code || 'GL';
}

function countryFlagMarkup(country) {
  const title = country?.chn || country?.eng || countryName(country?.id) || '全球';
  const code = countryFlagCode(country?.id, country).toLowerCase();
  if (code === 'gl') return '';
  return `<span class="auth-preview-flag" title="${escapeHTML(title)}" aria-label="${escapeHTML(title)}"><img src="/flags/${code}.svg" alt=""></span>`;
}
function canReplaceOrder(order) {
  return order?.Status === 'waiting' && heroCooldownRemaining(order) === 0;
}

function canCancelOrder(order) {
  return order?.Status === 'waiting' && !order?.Code && heroCooldownRemaining(order) === 0;
}

function canFinishOrder(order) {
  return order?.Status === 'waiting' || order?.Status === 'code_received';
}

function canResendCode(order) {
  return order?.Status === 'code_received';
}

function canContinuePayment(order) {
  return order?.Status === 'awaiting_payment';
}
function canRefreshOrder(order) {
  return ['paid', 'purchasing', 'waiting', 'replacing', 'code_received'].includes(order?.Status);
}

function heroCooldownRemaining(order) {
  if ((order?.UpstreamProvider || 'hero') !== 'hero') return 0;
  if (!order?.LastNumberAt) return 0;
  const last = Date.parse(order.LastNumberAt);
  if (!Number.isFinite(last)) return 0;
  return Math.max(0, Math.ceil((last + 120000 - Date.now()) / 1000));
}
function shouldRevealOrderPhone(order) {
  return ['waiting', 'replacing', 'code_received', 'finished'].includes(order?.Status);
}
function isRecentActiveOrder(order) {
  const createdAt = Date.parse(order?.CreatedAt || '');
  if (!Number.isFinite(createdAt)) return true;
  return (Date.now() - createdAt) <= 24 * 60 * 60 * 1000;
}
function orderId(order) { return String(order?.id ?? order?.ID ?? '').trim(); }
function orderPhoneKey(order) { return `${orderId(order)}:${order.Phone}`; }
function activePhoneOrder(order) { return order.Phone && ['waiting', 'replacing', 'code_received'].includes(order.Status); }
function notifiedPhoneKeys() {
  try { return new Set(JSON.parse(localStorage.getItem('phoneNotified') || '[]')); } catch { return new Set(); }
}
function saveNotifiedPhoneKeys(keys) {
  localStorage.setItem('phoneNotified', JSON.stringify([...keys].slice(-120)));
}
async function copyText(text) {
  if (navigator.clipboard?.writeText) {
    try {
      await navigator.clipboard.writeText(String(text ?? ''));
      return;
    } catch {}
  }
  const input = document.createElement('textarea');
  input.value = text;
  input.setAttribute('readonly', '');
  input.style.position = 'fixed';
  input.style.left = '-9999px';
  document.body.appendChild(input);
  input.select();
  if (!document.execCommand('copy')) throw new Error('copy failed');
  input.remove();
}
function showPhoneModal(order) {
  if (!order?.Phone) return;  $('#phoneModalMeta').textContent = `${serviceDisplayName(order.Service)} · ${orderCountryName(order)}`;
  const parts = splitPhoneNumber(order.Phone, order.Country);
  $('#phoneModalNumber').innerHTML = parts.prefix
    ? `<span class="phone-prefix">${escapeHTML(parts.prefix)}</span><span class="phone-main">${escapeHTML(parts.number)}</span>`
    : `<span class="phone-main">${escapeHTML(parts.number)}</span>`;
  $('#phoneModalCopy').onclick = async () => {
    try {
      await copyText(order.Phone);
      toast('号码已复制');
    } catch {
      toast('复制失败，请手动复制');
    }
  };
  $('#phoneModal').classList.remove('hidden');
}
function closePhoneModal() { $('#phoneModal').classList.add('hidden'); }
function maybeShowNewPhoneModal() {
  const keys = notifiedPhoneKeys();
  const order = state.orders.find(item => activePhoneOrder(item) && !keys.has(orderPhoneKey(item)));
  if (!order) return;
  keys.add(orderPhoneKey(order));
  saveNotifiedPhoneKeys(keys);
  showPhoneModal(order);
}

function serviceIcon(code, name = '') {
  const key = `${code} ${name}`.toLowerCase();
  if (code === 'gp' || key.includes('ticketmaster')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><defs><linearGradient id="tm-grad" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#d4145a"/><stop offset="1" stop-color="#7c3aed"/></linearGradient></defs><rect width="24" height="24" rx="6" fill="url(#tm-grad)"/><path fill="#fff" d="M6.7 8.1h10.6v2H13.2v6.2H10.8v-6.2H6.7zm8.3 0h2.3v8.2H15z"/></svg></span>';
  }
  if (code === 'tw' || key.includes('twitter/x') || key.includes('twitter') || key.includes(' x ')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#111"/><path fill="#fff" d="M14.35 10.68 19.64 4.5H18.4l-4.6 5.38-3.67-5.38H5.95l5.55 8.13-5.55 6.87h1.24l4.86-6.01 4.1 6.01h4.18zM8.02 5.62h1.62l6.33 9.25h-1.62z"/></svg></span>';
  }
  if (code === 'cq' || key.includes('mercado')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#00a5e5"/><path fill="#fff7cc" d="M12 5.2c2.12 0 4 .68 5.4 1.78c.72.56 1.28 1.21 1.66 1.93c.2.37.3.77.3 1.18c0 1.05-.7 1.86-1.82 2.62c-.9.6-1.8 1.15-2.7 1.73c-.48.31-.9.48-1.42.48c-.48 0-.9-.15-1.4-.44l-.72-.43l-.72.43c-.5.3-.92.44-1.4.44c-.52 0-.94-.17-1.42-.48c-.9-.58-1.8-1.14-2.7-1.73C4.9 11.95 4.2 11.14 4.2 10.1c0-.41.1-.81.3-1.18c.38-.72.94-1.37 1.66-1.93C8 5.88 9.88 5.2 12 5.2m-3.56 5.04c.56 0 1.09.22 1.73.78l.88.77l.18-.13c.34-.25.69-.37 1.05-.37c.36 0 .71.12 1.05.37l.18.13l.88-.77c.64-.56 1.17-.78 1.73-.78c.82 0 1.56.47 2.2 1.28c-.42.49-.96.95-1.63 1.39c-.88.58-1.78 1.12-2.67 1.7c-.28.18-.5.25-.74.25c-.22 0-.42-.06-.7-.22L12 13.73l-.87.63c-.28.16-.48.22-.7.22c-.24 0-.46-.07-.74-.25c-.9-.58-1.8-1.12-2.67-1.7c-.67-.44-1.21-.9-1.63-1.39c.64-.81 1.38-1.28 2.2-1.28"/><path fill="#fff" d="M8.9 16.2c.47 0 .89.13 1.53.5l.65.37c.33.19.57.26.92.26s.59-.07.92-.26l.65-.37c.64-.37 1.06-.5 1.53-.5c.83 0 1.61.39 2.54 1.23c-1.57 1.13-3.52 1.77-5.64 1.77s-4.07-.64-5.64-1.77c.93-.84 1.71-1.23 2.54-1.23"/></svg></span>';
  }
  if (code === 'hw' || key.includes('alipay') || key.includes('alibaba') || key.includes('1688')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><defs><linearGradient id="ali-pay-grad" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#ff7a45"/><stop offset="1" stop-color="#f7b500"/></linearGradient></defs><rect width="24" height="24" rx="6" fill="url(#ali-pay-grad)"/><path fill="#fff" d="M8.1 7.2h7.95v1.17h-3.3v1.18h2.9c-.08 1.2-.42 2.22-.98 3.08c.94.46 1.87 1 2.76 1.63l-.84 1.08a16.2 16.2 0 0 0-2.76-1.72c-.92.88-2.18 1.58-3.8 2.08l-.67-1.16c1.27-.35 2.33-.84 3.12-1.47c-.7-.28-1.4-.5-2.08-.7l.66-1.06c.72.18 1.48.43 2.27.74c.34-.48.56-1.02.66-1.61H8.1V9.55h3.39V8.37H8.1zm1.4 6.9h5.12v1.2H9.5z"/></svg></span>';
  }
  if (code === 'yw' || key.includes('grindr')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#f2c200"/><path fill="#111" d="M12 5.2c2.44 0 4.53 1.83 4.85 4.28c.9.38 1.53 1.27 1.53 2.31c0 .69-.28 1.31-.74 1.76c.1.29.16.6.16.93c0 1.61-1.31 2.92-2.92 2.92h-5.76A2.92 2.92 0 0 1 6.2 14.48c0-.33.05-.64.16-.93a2.47 2.47 0 0 1-.74-1.76c0-1.04.63-1.93 1.53-2.31A4.89 4.89 0 0 1 12 5.2m-1.88 4.1a.95.95 0 1 0 0 1.9a.95.95 0 0 0 0-1.9m3.76 0a.95.95 0 1 0 0 1.9a.95.95 0 0 0 0-1.9m-3.86 4.14c.45.68 1.14 1.05 1.98 1.05s1.53-.37 1.98-1.05z"/></svg></span>';
  }
  if (code === 'im' || key.includes('imo')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><defs><linearGradient id="imo-grad" x1="0" y1="0" x2="1" y2="1"><stop stop-color="#f04b7d"/><stop offset="1" stop-color="#8a3ffc"/></linearGradient></defs><rect width="24" height="24" rx="6" fill="url(#imo-grad)"/><path fill="#fff" d="M7.7 8.1h2.1v7.8H7.7zm5.15 0c2.45 0 4.15 1.6 4.15 3.9s-1.7 3.9-4.15 3.9s-4.15-1.6-4.15-3.9s1.7-3.9 4.15-3.9m0 1.9c-1.14 0-1.93.82-1.93 2s.79 2 1.93 2s1.93-.82 1.93-2s-.79-2-1.93-2"/></svg></span>';
  }
  if (code === 'wb' || key.includes('wechat') || key.includes('weixin')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#1aad19"/><path fill="#fff" d="M9.3 6.2c-3.08 0-5.58 2.02-5.58 4.51c0 1.42.8 2.68 2.05 3.5l-.52 2.09l2.2-1.1c.63.17 1.23.25 1.85.25c.18 0 .36-.01.53-.03c-.34-.57-.52-1.22-.52-1.91c0-2.49 2.5-4.51 5.58-4.51c.17 0 .33.01.49.02C14.56 7.39 12.14 6.2 9.3 6.2m-1.98 3.08a.7.7 0 1 1 0 1.4a.7.7 0 0 1 0-1.4m3.95 0a.7.7 0 1 1 0 1.4a.7.7 0 0 1 0-1.4m3.67.98c-2.51 0-4.54 1.56-4.54 3.49c0 1.93 2.03 3.49 4.54 3.49c.5 0 .99-.07 1.45-.2l1.73.86l-.4-1.58c.95-.63 1.54-1.58 1.54-2.57c0-1.93-2.03-3.49-4.54-3.49m-1.43 2.15a.57.57 0 1 1 0 1.14a.57.57 0 0 1 0-1.14m2.86 0a.57.57 0 1 1 0 1.14a.57.57 0 0 1 0-1.14"/></svg></span>';
  }
  if (code === 'am' || key.includes('amazon')) {
    return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#131a22"/><path fill="#fff" d="M8.35 14.1c0 1.07.9 1.7 2.17 1.7c.72 0 1.37-.2 1.93-.58c.11.15.25.34.43.58h1.72v-.24c-.36-.32-.43-.49-.43-1.01v-2.72c0-1.15.08-2.2-.77-3c-.67-.63-1.78-.86-2.64-.86c-1.66 0-3.51.62-3.89 2.68l1.68.18c.16-.67.64-.99 1.27-.99c.34 0 .72.12.93.41c.23.32.2.75.2 1.1v.19c-.95.1-2.18.18-3.1.58c-.95.43-1.5 1.19-1.5 1.98m2.1-.19c0-.44.24-.74.6-.94c.3-.17.76-.28 1.2-.36v.26c0 .49.02.9-.23 1.34c-.2.37-.54.59-.92.59c-.43 0-.65-.31-.65-.89"/><path fill="#f3a847" d="M17.3 15.92c-1.33.98-3.27 1.5-4.94 1.5c-2.34 0-4.45-.86-6.04-2.28c-.12-.11-.01-.26.13-.18c1.72 1 3.84 1.6 6.03 1.6c1.48 0 3.12-.31 4.62-.95c.23-.1.43.15.2.31m.56-.64c-.17-.21-1.1-.1-1.52-.05c-.13.02-.15-.1-.03-.18c.76-.54 2-.38 2.15-.2c.15.18-.04 1.42-.76 2.02c-.11.09-.22.04-.17-.08c.16-.39.5-1.27.33-1.51"/></svg></span>';
  }
  if (code === 'ub' || key.includes('uber')) return '<span class="service-icon brand" aria-hidden="true"><svg viewBox="0 0 24 24"><rect width="24" height="24" rx="6" fill="#000"/><path fill="#fff" d="M7.6 6.8v6.1c0 2.7 1.7 4.2 4.4 4.2s4.4-1.5 4.4-4.2V6.8h-2.1v5.9c0 1.4-.8 2.2-2.3 2.2s-2.3-.8-2.3-2.2V6.8zm11.3 4.2c-2 0-3.3 1.3-3.3 3.1s1.3 3.1 3.3 3.1c.9 0 1.7-.3 2.3-.8v-1.6c-.5.4-1.3.7-2.1.7c-.8 0-1.4-.3-1.6-.9h4v-.5c0-1.8-1-3-2.6-3m0 1.6c.7 0 1.1.4 1.2 1h-2.6c.1-.6.6-1 1.4-1"/></svg></span>';
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
  ['paypal', 'paypal'], ['uber', 'uber'], ['twitter/x', 'x-icon'], ['twitter', 'x-icon'], ['x.com', 'x-icon'],
  ['netflix', 'netflix-icon'], ['spotify', 'spotify-icon'], ['airbnb', 'airbnb-icon'],
  ['dropbox', 'dropbox'], ['cloudflare', 'cloudflare-icon'], ['openai', 'openai-icon'], ['chatgpt', 'openai-icon'],
  ['signal', 'signal'], ['slack', 'slack-icon'], ['notion', 'notion-icon'], ['zoom', 'zoom-icon']
];

function serviceBrand(key) {
  const code = key.split(' ', 1)[0];
  const codeBrand = {go: 'google', fb: 'facebook', ds: 'discord-icon', mb: 'yahoo', yah: 'yahoo', gh: 'github-icon', ms: 'microsoft-icon', pp: 'paypal', ub: 'uber'}[code];
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
function cookieConsentChoice() {
  try { return localStorage.getItem(COOKIE_CONSENT_KEY) || ''; } catch { return ''; }
}
function saveCookieConsentChoice(value) {
  try { localStorage.setItem(COOKIE_CONSENT_KEY, value); } catch {}
}
function updateCookieConsent() {
  const box = $('#cookieConsent');
  if (!box) return;
  const shouldShow = cookieConsentChoice() === '' && $('#auth') && !$('#auth').classList.contains('hidden');
  box.classList.toggle('hidden', !shouldShow);
  document.body.classList.toggle('cookie-consent-open', shouldShow);
}
function applyCookieConsent(choice) {
  saveCookieConsentChoice(choice);
  updateCookieConsent();
  toast(choice === 'accepted' ? '已接受 Cookie 提示' : '已仅保留必要 Cookie');
}

function readCatalogCache(country = '') {
  try {
    const parsed = JSON.parse(localStorage.getItem(CATALOG_CACHE_KEY) || '{}');
    const entry = parsed[String(country || '')];
    if (!entry || !entry.savedAt || !entry.data) return null;
    if ((Date.now() - Number(entry.savedAt)) > CATALOG_CACHE_TTL) return null;
    if (!hasMeaningfulCatalogData(entry.data)) return null;
    return entry.data;
  } catch {
    return null;
  }
}

function writeCatalogCache(country = '', data) {
  try {
    const parsed = JSON.parse(localStorage.getItem(CATALOG_CACHE_KEY) || '{}');
    parsed[String(country || '')] = {savedAt: Date.now(), data};
    localStorage.setItem(CATALOG_CACHE_KEY, JSON.stringify(parsed));
  } catch {}
}

function applyGlobalCatalogData(data) {
  const countries = (data.countries || []).filter(country => country.visible !== 0);
  const services = data.services || [];
  const offers = data.offers || [];
  state.globalCatalog = {countries, services, offers};
  state.countries = countries;
  state.allServices = services;
  state.services = services;
  state.offers = offers;
  renderAuthPreview();
}

function selectableCountries() {
  const selectedService = String(state.selectedService || '').trim();
  const countries = state.globalCatalog.countries || [];
  if (!selectedService) return countries;
  const available = new Set((state.globalCatalog.offers || []).filter(offer => String(offer.service) === selectedService).map(offer => String(offer.country)));
  return countries.filter(country => available.has(String(country.id)));
}

function syncSelectionState() {
  const countries = selectableCountries();
  return countries;
}

function renderCountryOptions(countries = selectableCountries()) {
  const countrySelect = $('#country');
  if (!countrySelect) return;
  const selectedCountry = String(state.selectedCountry || '').trim();
  countrySelect.innerHTML = '<option value="">不限国家（自动推荐）</option>' + countries
    .map(country => {
      const primary = country.chn || country.eng || countryName(country.id);
      return `<option value="${country.id}">${escapeHTML(primary)}</option>`;
    })
    .join('');
  if (selectedCountry && countries.some(country => String(country.id) === selectedCountry)) {
    countrySelect.value = selectedCountry;
    return;
  }
  if (!selectedCountry) {
    countrySelect.value = '';
  }
}

function servicePreviewCandidates(limit = 8) {
  const curated = [
    {code: 'tg', name: 'Telegram', note: '聊天账号'},
    {code: 'dr', name: 'OpenAI', note: 'AI 验证'},
    {code: 'tw', name: 'Twitter/X', note: '社交账号'},
    {code: 'go', name: 'Google', note: '邮箱验证'},
    {code: 'fb', name: 'Facebook', note: '社交账号'},
    {code: 'ig', name: 'Instagram', note: '社交账号'},
  ];
  return curated.slice(0, limit);
}

function servicePreviewNote(code, name) {
  const key = `${code} ${name}`.toLowerCase();
  if (code === 'dr' || key.includes('openai') || key.includes('chatgpt')) return 'AI 验证';
  if (code === 'tw' || key.includes('twitter') || key.includes('x')) return '社交账号';
  if (code === 'tg' || key.includes('telegram')) return '聊天账号';
  if (code === 'go' || key.includes('google') || key.includes('gmail')) return '邮箱验证';
  if (code === 'fb' || key.includes('facebook')) return '社交账号';
  if (code === 'ig' || key.includes('instagram')) return '社交账号';
  return '验证码';
}

function renderAuthPreview() {
  return;
}

async function boot() {
  loadAnnouncements().catch(() => {});
  await loadAuthConfig().catch(() => {});
  await loadCatalog().catch(() => {});
  renderAuthPreview();
  try {
    const me = await api('/api/me');
    showApp(me);
    await Promise.all([loadCatalog(), loadOrders()]);
    const orderID = new URLSearchParams(location.search).get('order');
    if (orderID) {
      toast('支付已返回，正在确认支付并取号');
      pollOrder(orderID);
    }
  } catch {
    $('#auth').classList.remove('hidden');
    updateCookieConsent();
  }
}

async function loadAuthConfig() {
  state.authConfig = await api('/api/auth/config');
  updateAuthVerificationUI();
}

function updateAuthVerificationUI() {
  const hasCaptcha = state.register && Boolean(state.authConfig.turnstileSiteKey);
  const needsEmailCode = state.register && state.authConfig.emailVerificationRequired && state.authConfig.emailVerificationAvailable;
  $('#verificationFields').classList.toggle('hidden', !hasCaptcha);
  $('#password').setAttribute('autocomplete', state.register ? 'new-password' : 'current-password');
  $('#authForm').setAttribute('action', state.register ? '/api/auth/register' : '/api/auth/login');
  const emailLabel = document.querySelector('#verificationFields > label');
  const verificationRow = document.querySelector('#verificationFields .verification-row');
  const turnstileHint = document.querySelector('#turnstileWrap small');
  if (emailLabel) emailLabel.classList.toggle('hidden', !needsEmailCode);
  if (verificationRow) verificationRow.classList.toggle('hidden', !needsEmailCode);
  if (turnstileHint) turnstileHint.textContent = needsEmailCode ? '发送验证码前请完成人机验证' : '注册前请完成人机验证';
  $('#emailCode').required = needsEmailCode;
  if (hasCaptcha) loadTurnstile();
}

function loadTurnstile() {
  if (!state.register || !state.authConfig.turnstileSiteKey || state.turnstileWidget !== null) return;
  const render = () => {
    if (!globalThis.turnstile || state.turnstileWidget !== null) return;
    state.turnstileWidget = globalThis.turnstile.render('#turnstileWidget', {sitekey: state.authConfig.turnstileSiteKey, theme: 'light'});
  };
  if (globalThis.turnstile) { render(); return; }
  let script = document.querySelector('script[data-turnstile]');
  if (!script) {
    script = document.createElement('script');
    script.src = 'https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit';
    script.async = true;
    script.defer = true;
    script.dataset.turnstile = 'true';
    document.head.appendChild(script);
  }
  script.addEventListener('load', render, {once: true});
}

async function loadAnnouncements() {
  const items = await api('/api/announcements');
  const banner = $('#announcementBanner');
  if (!items.length) return;
  const item = items[0];
  banner.innerHTML = `<button type="button" aria-label="关闭公告">×</button><b>${escapeHTML(item.title)}</b><span>${escapeHTML(item.body)}</span>`;
  banner.classList.remove('hidden');
  banner.querySelector('button').onclick = () => banner.classList.add('hidden');
}

function showApp(data) {
  state.user = data.user;
  state.liveSmsPurchaseEnabled = Boolean(data.liveSmsPurchaseEnabled);
  $('#auth').classList.add('hidden');
  $('#app').classList.remove('hidden');
  const email = data.user?.email || '账户';
  const initials = accountInitials(email);
  $('#accountEmail').textContent = email;
  $('#accountAvatar').textContent = initials;
  $('#profileAvatar').textContent = initials;
  $('#accountNav').classList.remove('hidden');
  $('#supportOpen').classList.remove('hidden');
  updateCookieConsent();
  if (!state.liveSmsPurchaseEnabled) {
    $('#buy').textContent = '支付取号演示模式';
    $('#stock').textContent = '演示环境暂不分配真实号码';
  }
}

function accountInitials(email) {
  const prefix = String(email || 'U').split('@')[0];
  return (prefix.match(/[a-z0-9]/gi) || ['U']).slice(0, 2).join('').toUpperCase();
}

let supportTimer;
$('#supportOpen').onclick = async () => {
  $('#supportPanel').classList.remove('hidden');
  await Promise.all([loadSupportSettings(), loadSupportMessages()]).catch(error => toast(error.message));
  clearInterval(supportTimer);
  supportTimer = setInterval(() => loadSupportMessages().catch(() => {}), 5000);
};
$('#supportClose').onclick = () => { $('#supportPanel').classList.add('hidden'); clearInterval(supportTimer); };
async function loadSupportSettings() {
  const settings = await api('/api/settings');
  $('#supportTitle').textContent = settings.contactTitle || '在线客服';
  $('#supportHours').textContent = settings.supportHours || '';
  const link = $('#contactLink');
  if (settings.contactValue) {
    link.textContent = settings.contactValue;
    if (settings.contactURL) link.href = settings.contactURL;
    else link.removeAttribute('href');
    link.classList.remove('hidden');
  } else link.classList.add('hidden');
}
async function loadSupportMessages() {
  const messages = await api('/api/support');
  const box = $('#supportMessages');
  box.innerHTML = messages.length ? messages.map(message => `<div class="support-message ${escapeHTML(message.sender)}">${escapeHTML(message.body)}<small>${new Date(message.createdAt).toLocaleString()}</small></div>`).join('') : '<p>有问题可以直接留言，我们会尽快回复。</p>';
  box.scrollTop = box.scrollHeight;
}
$('#supportForm').onsubmit = async event => {
  event.preventDefault();
  const body = $('#supportBody').value.trim();
  if (!body) return;
  await api('/api/support', {method: 'POST', body: JSON.stringify({body})});
  $('#supportBody').value = '';
  await loadSupportMessages();
};

$('#toggleAuth').onclick = () => {
  state.register = !state.register;
  $('#authTitle').textContent = state.register ? '创建账户' : '登录账户';
  $('#authSubmit').textContent = state.register ? '创建账户' : '登录';
  $('#toggleAuth').textContent = state.register ? '已有账户？返回登录' : '没有账户？立即注册';
  updateAuthVerificationUI();
  updateCookieConsent();
};
$('#acceptCookies').onclick = () => applyCookieConsent('accepted');
$('#rejectCookies').onclick = () => applyCookieConsent('essential-only');

$('#sendEmailCode').onclick = async () => {
  const button = $('#sendEmailCode');
  $('#authError').textContent = '';
  const token = state.turnstileWidget !== null && globalThis.turnstile ? globalThis.turnstile.getResponse(state.turnstileWidget) : '';
  if (!token) { $('#authError').textContent = '请先完成人机验证'; return; }
  button.disabled = true;
  try {
    await api('/api/auth/email-code', {method: 'POST', body: JSON.stringify({email: $('#email').value, turnstileToken: token})});
    toast('验证码已发送，请检查邮箱');
    if (globalThis.turnstile) globalThis.turnstile.reset(state.turnstileWidget);
    let remaining = 60;
    button.textContent = `${remaining} 秒后重发`;
    const timer = setInterval(() => {
      remaining--;
      button.textContent = remaining > 0 ? `${remaining} 秒后重发` : '重新发送';
      if (remaining <= 0) { clearInterval(timer); button.disabled = false; }
    }, 1000);
  } catch (error) {
    $('#authError').textContent = error.message;
    button.disabled = false;
  }
};
$('#authForm').onsubmit = async event => {
  event.preventDefault();
  $('#authError').textContent = '';
  try {
    const payload = {Email: $('#email').value, Password: $('#password').value};
    if (state.register && state.authConfig.emailVerificationRequired && state.authConfig.emailVerificationAvailable) {
      payload.Code = $('#emailCode').value;
    }
    if (state.register && (!state.authConfig.emailVerificationRequired || !state.authConfig.emailVerificationAvailable) && state.authConfig.turnstileSiteKey) {
      const token = state.turnstileWidget !== null && globalThis.turnstile ? globalThis.turnstile.getResponse(state.turnstileWidget) : '';
      if (!token) { $('#authError').textContent = '请先完成人机验证'; return; }
      payload.TurnstileToken = token;
    }
    await api(`/api/auth/${state.register ? 'register' : 'login'}`, {method: 'POST', body: JSON.stringify(payload)});
    showApp(await api('/api/me'));
    await Promise.all([loadCatalog(), loadOrders()]);
  } catch (error) {
    $('#authError').textContent = error.message;
    if (state.register && state.turnstileWidget !== null && globalThis.turnstile) {
      globalThis.turnstile.reset(state.turnstileWidget);
    }
  }
};
$('#logout').onclick = async () => { await api('/api/auth/logout', {method: 'POST'}); location.reload(); };

$('#accountButton').onclick = event => {
  event.stopPropagation();
  const dropdown = $('#accountDropdown');
  const opening = dropdown.classList.contains('hidden');
  dropdown.classList.toggle('hidden', !opening);
  $('#accountButton').setAttribute('aria-expanded', String(opening));
};
document.addEventListener('click', event => {
  if (!$('#accountNav').contains(event.target)) {
    $('#accountDropdown').classList.add('hidden');
    $('#accountButton').setAttribute('aria-expanded', 'false');
  }
});

$('#openProfile').onclick = async () => {
  $('#accountDropdown').classList.add('hidden');
  $('#accountButton').setAttribute('aria-expanded', 'false');
  $('#profileModal').classList.remove('hidden');
  $('#profileOrderRows').innerHTML = '<p class="profile-loading">正在加载...</p>';
  try {
    const data = await api('/api/profile');
    renderProfile(data);
  } catch (error) {
    $('#profileOrderRows').innerHTML = `<p class="profile-empty">${escapeHTML(error.message)}</p>`;
  }
};

function renderProfile(data) {
  const profile = data.profile;
  $('#profileEmail').textContent = profile.email;
  $('#profileAvatar').textContent = accountInitials(profile.email);
  $('#profileCreatedAt').textContent = `注册于 ${new Date(profile.createdAt).toLocaleDateString()}`;
  $('#profileOrdersTotal').textContent = profile.ordersTotal;
  $('#profileOrdersSuccessful').textContent = profile.ordersSuccessful;
  $('#profileSpent').textContent = money(profile.spentFen);
  const rows = data.orders || [];
  const maskClosedPhone = order => ['admin_closed', 'cancelled'].includes(order?.Status);
  $('#profileOrderRows').innerHTML = rows.length
    ? rows.map(order => `<div class="profile-order-row"><span><b>${escapeHTML(serviceDisplayName(order.Service))}</b><small>${escapeHTML(orderCountryName(order))}${order.Phone && !maskClosedPhone(order) ? ` · ${escapeHTML(formatPhoneNumber(order.Phone, order.Country))}` : ''}</small></span><span class="order-status">${escapeHTML(status(order.Status))}</span><span>${new Date(order.CreatedAt).toLocaleString()}</span><strong>${money(order.PriceFen)}</strong></div>`).join('')
    : '<p class="profile-empty">暂无历史订单</p>';
}

function closeProfile() { $('#profileModal').classList.add('hidden'); }
$('#profileClose').onclick = closeProfile;
$('#profileModal').onclick = event => { if (event.target === $('#profileModal')) closeProfile(); };
document.addEventListener('keydown', event => { if (event.key === 'Escape') closeProfile(); });

async function loadCatalog() {
  const data = await api('/api/catalog');
  applyGlobalCatalogData(data);
  if (state.selectedCountry) {
    await loadCountryCatalog(state.selectedCountry);
  } else {
    renderServices();
  }
  return data;
}

async function loadCountryCatalog(countryID) {
  const selectedCountry = String(countryID || '').trim();
  state.countryRequestSerial += 1;
  const requestID = state.countryRequestSerial;
  if (!selectedCountry) {
    state.countryCatalog = null;
    state.countryCatalogLoading = false;
    renderServices();
    return null;
  }
  state.countryCatalogLoading = true;
  renderServices();
  try {
    const data = await api(`/api/catalog?country=${encodeURIComponent(selectedCountry)}`);
    if (requestID !== state.countryRequestSerial) return null;
    state.countryCatalog = {
      country: selectedCountry,
      countries: data.countries || [],
      services: data.services || [],
      offers: data.offers || [],
    };
    return data;
  } finally {
    if (requestID === state.countryRequestSerial) {
      state.countryCatalogLoading = false;
      renderServices();
    }
  }
}

$('#country').onchange = async () => {
  state.selectedCountry = String($('#country').value || '').trim();
  await loadCountryCatalog(state.selectedCountry);
};
$('#search').oninput = renderServices;

function effectiveOffers() {
  if (state.selectedCountry && state.countryCatalog?.country === state.selectedCountry) {
    return state.countryCatalog.offers || [];
  }
  if (!state.selectedCountry) return state.globalCatalog.offers || [];
  return (state.globalCatalog.offers || []).filter(offer => String(offer.country) === state.selectedCountry);
}

function visibleOffers() {
  return effectiveOffers();
}

function effectiveServices() {
  return state.allServices.length ? state.allServices : (state.globalCatalog.services || []);
}

function renderServices() {
  const list = $('#service');
  const previousScrollTop = list.scrollTop;
  const query = $('#search').value.toLowerCase();
  const countries = syncSelectionState();
  const offers = effectiveOffers();
  const available = new Map(offers.map(offer => [offer.service, offer]));
  const sourceServices = effectiveServices();
  const services = state.selectedService
    ? sourceServices.filter(service => service.code === state.selectedService)
    : sourceServices
        .filter(service => !state.selectedCountry || available.has(service.code))
        .filter(service => (`${service.name} ${service.code}`).toLowerCase().includes(query));
  list.innerHTML = services.length ? services.map(service => {
    return `<button type="button" class="service-option${state.selectedService === service.code ? ' selected' : ''}" data-service="${escapeHTML(service.code)}" role="option" aria-selected="${state.selectedService === service.code}">${serviceIcon(service.code, service.name)}<span><b>${escapeHTML(service.name)}</b><small>${escapeHTML(service.code)}</small></span></button>`;
  }).join('') : `<div class="service-loading">${state.countryCatalogLoading ? '正在加载服务...' : (state.selectedCountry ? '当前国家暂无可用服务' : '暂无可用服务')}</div>`;
  document.querySelectorAll('.service-option').forEach(button => button.onclick = () => selectService(button.dataset.service));
  requestAnimationFrame(() => { list.scrollTop = previousScrollTop; });
  renderCountryOptions(countries);
  updateSelectedServiceMeta();
  selectOffer();
}
function selectService(code) {
  state.selectedService = code;
  renderServices();
}
function updateSelectedServiceMeta() {
  const meta = $('#selectedServiceMeta');
  if (!meta) return;
  if (!state.selectedService) {
    meta.classList.add('hidden');
    meta.innerHTML = '';
    return;
  }
  const service = state.services.find(item => item.code === state.selectedService) || state.allServices.find(item => item.code === state.selectedService);
  const offer = visibleOffers().find(item => item.service === state.selectedService) || state.offers.find(item => item.service === state.selectedService);
  const name = service?.name || serviceDisplayName(state.selectedService);
  const note = offer
    ? (state.selectedCountry ? '当前国家可下单' : '已锁定当前服务，请选择国家查看报价')
    : (state.selectedCountry ? '该国家下暂无库存，请切换国家' : '请选择支持该服务的国家');
  meta.classList.remove('hidden');
  meta.innerHTML = `${serviceIcon(state.selectedService, name)}<span><b>当前选择：${escapeHTML(name)}</b><small>${escapeHTML(note)}</small></span><button id="clearSelectedService" class="secondary service-clear" type="button">重新选择</button>`;
  $('#clearSelectedService').onclick = () => {
    state.selectedService = '';
    renderServices();
  };
}
function selectOffer() {
  const offer = visibleOffers().find(item => item.service === state.selectedService) || null;
  $('#buy').textContent = state.liveSmsPurchaseEnabled ? '支付并取号' : '支付取号演示模式';
  $('#buy').disabled = !offer || !state.liveSmsPurchaseEnabled;
  $('#price').textContent = state.selectedService ? (offer ? money(offer.priceFen) : (state.selectedCountry ? '该国家下暂无库存' : '请选择国家查看报价')) : '请选择服务';
  const cheapestCountry = offer ? Array.from($('#country').options).find(option => option.value === String(offer.country))?.textContent || offer.country : '';
  const scope = offer && !state.selectedCountry ? ` · 推荐国家 ${cheapestCountry}` : '';
  $('#stock').textContent = offer
    ? (state.liveSmsPurchaseEnabled
      ? `实时库存 ${offer.count} 个${scope}`
      : `实时库存 ${offer.count} 个${scope} · 演示环境`)
    : (state.selectedService
      ? (state.selectedCountry ? '该国家下当前服务暂无库存，请切换其他国家继续查看' : '请选择国家查看该服务报价')
      : '');
  updateSelectedServiceMeta();
}

$('#buy').onclick = async () => {
  const button = $('#buy');
  button.disabled = true;
  button.textContent = '正在创建支付订单...';
  try {
    const order = await api('/api/orders', {method: 'POST', body: JSON.stringify({Country: state.selectedCountry, Service: state.selectedService, payType: Number($('#payType').value)})});
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
  const recentOrders = state.orders.filter(isRecentActiveOrder);
  if (!recentOrders.length) {
    box.innerHTML = '<div class="empty">最近 24 小时没有活跃订单。更早的订单可在个人中心查看。</div>';
    return;
  }
  box.innerHTML = recentOrders.map(order => {
    const serviceName = serviceDisplayName(order.Service);
    const replaceText = ['waiting', 'replacing', 'code_received'].includes(order.Status) ? replaceAttemptsText(order.ReplaceAttempts) : '';
    const cooldownRemaining = heroCooldownRemaining(order);
    const phoneParts = order.Phone ? splitPhoneNumber(order.Phone, order.Country) : null;
    const phone = order.Phone && shouldRevealOrderPhone(order)
      ? `<button class="phone-pill show-phone" type="button" data-id="${escapeHTML(orderId(order))}" title="\u67e5\u770b\u53f7\u7801">${phoneParts?.prefix ? `<span class="phone-prefix">${escapeHTML(phoneParts.prefix)}</span>` : ''}<span class="phone-main">${escapeHTML(phoneParts?.number || '')}</span></button><button class="copy-phone" type="button" data-phone="${escapeHTML(formatPhoneNumber(order.Phone, order.Country))}">\u590d\u5236</button>`
      : `<b>${escapeHTML(serviceName)}</b>`;
    const continueButton = canContinuePayment(order)
      ? iconActionButton('continue-pay', orderId(order), '继续支付', `
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M13 5l7 7-7 7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            <path d="M4 12h16" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
          </svg>
        `)
      : '';
    const replaceButton = canReplaceOrder(order)
      ? iconActionButton('replace-one', orderId(order), '更换号码', `
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M4 7h12a4 4 0 0 1 0 8H7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            <path d="M7 11l-4 4 4 4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        `)
      : '';
    const cancelButton = canCancelOrder(order)
      ? iconActionButton('cancel-one', orderId(order), '取消购买', `
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M6 6l12 12M18 6 6 18" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round"/>
          </svg>
        `)
      : '';
    const finishButton = canFinishOrder(order)
      ? iconActionButton('finish-one', orderId(order), '完成', `
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M5 12.5l4.5 4.5L19 7.5" fill="none" stroke="currentColor" stroke-width="2.2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        `)
      : '';
    const resendButton = canResendCode(order)
      ? iconActionButton('resend-one', orderId(order), '重新发送验证码', `
          <svg viewBox="0 0 24 24" aria-hidden="true">
            <path d="M4 11a8 8 0 0 1 13.66-5.66L20 7.5V4" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
            <path d="M20 13a8 8 0 0 1-13.66 5.66L4 16.5V20" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
          </svg>
        `)
      : '';
    const refundTag = order.Refunded && ['admin_closed', 'cancelled'].includes(order.Status)
      ? '<small class="refund-tag">\u5df2\u9000\u6b3e</small>'
      : '';
    const validityTag = ['waiting', 'replacing', 'code_received'].includes(order.Status)
      ? '<small class="order-validity">20 分钟有效</small>'
      : '';
    const cooldownTag = cooldownRemaining > 0
      ? `<small class="order-validity">HeroSMS ${cooldownRemaining} 秒后可换号/关闭</small>`
      : '';
    const refreshButton = canRefreshOrder(order)
      ? iconActionButton('refresh-one', orderId(order), '刷新', `
      <svg viewBox="0 0 24 24" aria-hidden="true">
        <path d="M20 11a8 8 0 1 0 2 5.5" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round"/>
        <path d="M20 4v7h-7" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"/>
      </svg>
    `)
      : '';
    return `<article class="order"><div class="order-service">${serviceIcon(order.Service, serviceName)}<span><span class="${order.Phone ? 'order-phone' : ''}">${phone}</span><small>${escapeHTML(orderCountryName(order))}${replaceText}</small></span></div><div><span class="badge">${status(order.Status)}</span>${refundTag}<small>${new Date(order.CreatedAt).toLocaleString()}</small>${validityTag}${cooldownTag}</div><div>${order.Code ? `<code>${escapeHTML(order.Code)}</code>` : '<small>\u7b49\u5f85\u9a8c\u8bc1\u7801</small>'}<b>${money(order.PriceFen)}</b></div><div class="order-actions">${continueButton}${refreshButton}${replaceButton}${cancelButton}${resendButton}${finishButton}</div></article>`;
  }).join('');
  document.querySelectorAll('.continue-pay').forEach(button => button.onclick = () => continuePayment(button.dataset.id));
  document.querySelectorAll('.refresh-one').forEach(button => button.onclick = () => refreshOrder(button.dataset.id));
  document.querySelectorAll('.replace-one').forEach(button => button.onclick = () => manualReplace(button.dataset.id));
  document.querySelectorAll('.cancel-one').forEach(button => button.onclick = () => cancelOrder(button.dataset.id));
  document.querySelectorAll('.resend-one').forEach(button => button.onclick = () => resendCode(button.dataset.id));
  document.querySelectorAll('.finish-one').forEach(button => button.onclick = () => finishOrder(button.dataset.id));
  document.querySelectorAll('.show-phone').forEach(button => button.onclick = () => showPhoneModal(state.orders.find(order => orderId(order) === button.dataset.id)));
  document.querySelectorAll('.copy-phone').forEach(button => button.onclick = async () => {
    try {
      await copyText(button.dataset.phone);
      toast('\u53f7\u7801\u5df2\u590d\u5236');
    } catch {
      toast('\u590d\u5236\u5931\u8d25\uff0c\u8bf7\u624b\u52a8\u590d\u5236');
    }
  });
  maybeShowNewPhoneModal();
}
function iconActionButton(className, id, label, icon) {
  return `<button class="order-icon-btn ${className}" type="button" data-id="${id}" title="${label}" aria-label="${label}">${icon}</button>`;
}
function status(value) {
  return ({awaiting_payment: '\u5f85\u652f\u4ed8', payment_failed: '\u652f\u4ed8\u5931\u8d25', paid: '\u5df2\u652f\u4ed8\u5f85\u53d6\u53f7', purchasing: '\u53d6\u53f7\u4e2d', waiting: '\u7b49\u5f85\u9a8c\u8bc1\u7801', replacing: '\u6362\u53f7\u4e2d', code_received: '\u5df2\u6536\u7801', cancelled: '\u5df2\u53d6\u6d88', admin_closed: '\u7ba1\u7406\u5458\u5173\u95ed', purchase_failed: '\u53d6\u53f7\u5931\u8d25', replace_failed: '\u6362\u53f7\u5931\u8d25', finished: '\u5df2\u5b8c\u6210'})[value] || value;
}
async function refreshOrder(id) {
  try {
    await loadOrders();
    const order = state.orders.find(item => orderId(item) === String(id));
    if (order) toast(order.Code ? '验证码已同步' : '订单状态已刷新');
    else toast('订单列表已同步');
  } catch (error) {
    toast(error.message);
  }
}
async function continuePayment(id) {
  try {
    toast('\u6b63\u5728\u8df3\u8f6c\u652f\u4ed8\u9875');
    const result = await api(`/api/orders/${id}/checkout`);
    location.href = result.checkoutUrl;
  } catch (error) {
    toast(error.message);
  }
}
async function manualReplace(id) {
  try {
    const confirmed = window.confirm('更换号码会先释放当前上游号码，再重新租赁一个新号码；这不是退款。确认更换吗？');
    if (!confirmed) return;
    await api(`/api/orders/${id}/replace`, {method: 'POST'});
    toast('\u5df2\u4e3a\u4f60\u66f4\u6362\u53f7\u7801');
    await loadOrders();
  } catch (error) {
    toast(error.message);
  }
}
async function cancelOrder(id) {
  try {
    const confirmed = window.confirm('取消购买会先释放当前上游号码，再按退款规则尝试 50pay 原路退款。确认取消吗？');
    if (!confirmed) return;
    const result = await api(`/api/orders/${id}/cancel`, {method: 'POST'});
    if (result.refunded) toast('订单已取消，已原路退款');
    else if (result.reason === 'refund_threshold_reached') toast('订单已取消；短时间退款次数已达上限，未自动退款');
    else toast('订单已取消');
    await loadOrders();
  } catch (error) {
    toast(error.message);
  }
}
async function finishOrder(id) {
  try {
    const confirmed = window.confirm('完成后会结束当前接码订单，并停止继续等待验证码。');
    if (!confirmed) return;
    await api(`/api/orders/${id}/finish`, {method: 'POST'});
    toast('订单已完成');
    await loadOrders();
  } catch (error) {
    toast(error.message);
  }
}
async function resendCode(id) {
  try {
    const confirmed = window.confirm('重新发送验证码会向上游请求新的短信，并继续等待新的验证码。确认继续吗？');
    if (!confirmed) return;
    await api(`/api/orders/${id}/resend`, {method: 'POST'});
    toast('已请求重新发送验证码');
    await loadOrders();
  } catch (error) {
    toast(error.message);
  }
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
$('#phoneModalClose').onclick = closePhoneModal;
$('#phoneModalOk').onclick = closePhoneModal;
$('#phoneModal').onclick = event => { if (event.target === $('#phoneModal')) closePhoneModal(); };
setInterval(() => {
  if (!$('#app').classList.contains('hidden') && state.orders.some(order => ['paid', 'purchasing', 'waiting'].includes(order.Status))) loadOrders().catch(() => {});
}, 5000);

boot();

