const $ = selector => document.querySelector(selector);
const adminState = {view: "dashboard", userId: 0, threads: [], announcements: []};

async function request(path, options = {}) {
  const response = await fetch(path, {credentials: "same-origin", headers: {"content-type": "application/json"}, ...options});
  const data = await response.json().catch(() => ({}));
  if (!response.ok) throw new Error(data.error || `\u8bf7\u6c42\u5931\u8d25 ${response.status}`);
  return data;
}

function esc(value) {
  return String(value ?? "").replace(/[&<>'"]/g, char => ({
    "&": "&amp;",
    "<": "&lt;",
    ">": "&gt;",
    "'": "&#39;",
    "\"": "&quot;",
  }[char]));
}

function money(fen) { return `\u00a5${(Number(fen || 0) / 100).toFixed(2)}`; }
function date(value) { return value ? new Date(value).toLocaleString("zh-CN") : "-"; }
function toast(text) {
  const box = $("#toast");
  box.textContent = text;
  box.classList.add("show");
  setTimeout(() => box.classList.remove("show"), 2200);
}
function status(value) { return `<span class="status">${esc(value)}</span>`; }
function closableOrder(order) { return ["awaiting_payment", "paid", "purchasing", "waiting", "replacing"].includes(order.status); }

$("#loginForm").onsubmit = async event => {
  event.preventDefault();
  $("#loginError").textContent = "";
  try {
    await request("/api/admin/login", {method: "POST", body: JSON.stringify({Email: $("#email").value, Password: $("#password").value})});
    localStorage.setItem("adminLoginPending", "1");
    $("#loginError").textContent = "\u767b\u5f55\u6210\u529f\uff0c\u6b63\u5728\u8fdb\u5165\u540e\u53f0...";
    location.replace(`/admin.html?session=${Date.now()}`);
  } catch (error) {
    $("#loginError").textContent = error.message;
  }
};

$("#logout").onclick = async () => {
  await request("/api/admin/logout", {method: "POST"});
  location.reload();
};

$("#refresh").onclick = () => loadCurrent().catch(error => toast(error.message));

const titles = {
  dashboard: "\u6570\u636e\u6982\u89c8",
  ordersView: "\u8ba2\u5355\u7ba1\u7406",
  payments: "\u652f\u4ed8\u6d41\u6c34",
  emailLogs: "\u90ae\u4ef6\u65e5\u5fd7",
  users: "\u7528\u6237\u7ba1\u7406",
  announcements: "\u516c\u544a\u7ba1\u7406",
  support: "\u5ba2\u670d\u804a\u5929",
  audit: "\u64cd\u4f5c\u5ba1\u8ba1",
  settings: "\u7cfb\u7edf\u8bbe\u7f6e",
};

document.querySelectorAll("nav [data-view]").forEach(button => {
  button.onclick = () => switchView(button.dataset.view);
});

function switchView(view) {
  adminState.view = view;
  document.querySelectorAll(".view").forEach(element => element.classList.toggle("hidden", element.id !== view));
  document.querySelectorAll("nav [data-view]").forEach(element => element.classList.toggle("active", element.dataset.view === view));
  $("#viewTitle").textContent = titles[view];
  loadCurrent().catch(error => toast(error.message));
}

function showPanel() {
  $("#login").classList.add("hidden");
  $("#panel").classList.remove("hidden");
  loadOverview().catch(error => {
    if (error.message.includes("admin login")) location.reload();
    else toast(error.message);
  });
  loadThreads().catch(() => {});
}

function loadCurrent() {
  return ({
    dashboard: loadOverview,
    ordersView: loadOrders,
    payments: loadPayments,
    emailLogs: loadEmailLogs,
    users: loadUsers,
    announcements: loadAnnouncements,
    support: loadThreads,
    audit: loadAudit,
    settings: loadSettings,
  })[adminState.view]();
}

function orderRows(orders) {
  return orders.length
    ? orders.map(order => `
      <tr>
        <td class="order-id-cell">${esc(order.id.slice(0, 12))}</td>
        <td class="order-email-cell" title="${esc(order.email)}">${esc(order.email)}</td>
        <td>${esc(order.country)} / ${esc(order.service)}</td>
        <td class="order-phone-cell" title="${esc(order.phone || "-")}">${esc(order.phone || "-")}</td>
        <td>${esc(order.provider)}</td>
        <td>${money(order.priceFen)}</td>
        <td>${status(order.status)}</td>
        <td>${date(order.createdAt)}</td>
        <td class="order-action-cell">${closableOrder(order) ? `<button class="action danger order-close" data-id="${order.id}">\u5173\u95ed\u8ba2\u5355</button>` : "-"}</td>
      </tr>`).join("")
    : '<tr><td colspan="9" class="muted">\u6682\u65e0\u8ba2\u5355</td></tr>';
}

async function loadOverview() {
  const data = await request("/api/admin/overview");
  const stats = data.stats;
  $("#ordersTotal").textContent = stats.ordersTotal;
  $("#ordersToday").textContent = stats.ordersToday;
  $("#ordersSuccessful").textContent = stats.ordersSuccessful;
  $("#revenue").textContent = money(stats.revenueFen);
  $("#usersTotal").textContent = stats.usersTotal;
  renderBalance("hero", data.balances.hero);
  renderBalance("smsman", data.balances.smsman);
  $("#recentOrders").innerHTML = orderRows(data.orders);
}

function renderBalance(id, item) {
  const value = $(`#${id}Balance`);
  const error = $(`#${id}Error`);
  if (!item) {
    value.textContent = "\u672a\u914d\u7f6e";
    error.textContent = "";
    return;
  }
  value.textContent = item.available ? `${Number(item.amount).toFixed(2)} ${item.currency}` : "\u8bfb\u53d6\u5931\u8d25";
  error.textContent = item.error || "";
}

async function loadOrders() {
  const query = new URLSearchParams({q: $("#orderQuery").value, status: $("#orderStatus").value});
  $("#allOrders").innerHTML = orderRows(await request(`/api/admin/orders?${query}`));
  document.querySelectorAll(".order-close").forEach(button => button.onclick = () => closeOrder(button));
}
$("#searchOrders").onclick = loadOrders;

async function closeOrder(button) {
  if (!confirm("\u786e\u8ba4\u5173\u95ed\u8fd9\u4e2a\u8ba2\u5355\uff1f\u7cfb\u7edf\u4f1a\u5c1d\u8bd5\u53d6\u6d88\u4e0a\u6e38\u53f7\u7801\uff0c\u5e76\u6309\u8bbe\u5b9a\u89c4\u5219\u539f\u8def\u9000\u6b3e\u3002")) return;
  const result = await request(`/api/admin/orders/${button.dataset.id}/close`, {method: "POST"});
  if (result.refunded) toast("\u8ba2\u5355\u5df2\u5173\u95ed\uff0c\u5df2\u539f\u8def\u9000\u6b3e");
  else if (result.reason === "refund_threshold_reached") toast("\u8ba2\u5355\u5df2\u5173\u95ed\uff0c\u4f46\u8d85\u8fc7\u9000\u6b3e\u6b21\u6570\u9608\u503c\uff0c\u672a\u81ea\u52a8\u9000\u6b3e");
  else toast("\u8ba2\u5355\u5df2\u5173\u95ed");
  await loadOrders();
}

async function loadPayments() {
  const query = new URLSearchParams({q: $("#paymentQuery").value, status: $("#paymentStatus").value});
  const items = await request(`/api/admin/payments?${query}`);
  $("#paymentRows").innerHTML = items.length
    ? items.map(item => `
      <tr>
        <td>${esc(item.id.slice(0, 12))}</td>
        <td>${esc(item.email)}</td>
        <td>${esc(item.orderId.slice(0, 12))}</td>
        <td>${esc(item.provider)} / ${esc(item.payType)}</td>
        <td>${money(item.amountFen)}</td>
        <td>${status(item.status)}</td>
        <td>${date(item.createdAt)}</td>
      </tr>`).join("")
    : '<tr><td colspan="7" class="muted">\u6682\u65e0\u652f\u4ed8\u8bb0\u5f55</td></tr>';
}
$("#searchPayments").onclick = loadPayments;

async function loadEmailLogs() {
  const items = await request("/api/admin/email-logs");
  $("#emailLogRows").innerHTML = items.length
    ? items.map(item => `
      <tr>
        <td title="${esc(item.email)}">${esc(item.email)}</td>
        <td>${esc(item.provider)}</td>
        <td title="${esc(item.sender)}">${esc(item.sender)}</td>
        <td>${status(item.status)}</td>
        <td title="${esc(item.error || "-")}">${esc(item.error || "-")}</td>
        <td>${date(item.createdAt)}</td>
      </tr>`).join("")
    : '<tr><td colspan="6" class="muted">\u6682\u65e0\u90ae\u4ef6\u65e5\u5fd7</td></tr>';
}

async function loadUsers() {
  const items = await request(`/api/admin/users?q=${encodeURIComponent($("#userQuery").value)}`);
  $("#userRows").innerHTML = items.length
    ? items.map(user => `
      <tr>
        <td>${user.id}</td>
        <td>${esc(user.email)}</td>
        <td>${user.orders}</td>
        <td>${money(user.spentFen)}</td>
        <td>${money(user.balanceFen)}</td>
        <td>${date(user.createdAt)}</td>
        <td>${user.disabled ? '<span class="status">\u5df2\u5c01\u7981</span>' : '\u6b63\u5e38'}</td>
        <td><button class="action ${user.disabled ? "" : "danger"} user-toggle" data-id="${user.id}" data-disabled="${!user.disabled}">${user.disabled ? '\u89e3\u5c01' : '\u5c01\u7981'}</button></td>
      </tr>`).join("")
    : '<tr><td colspan="8" class="muted">\u6682\u65e0\u7528\u6237</td></tr>';
  document.querySelectorAll(".user-toggle").forEach(button => button.onclick = () => toggleUser(button));
}
$("#searchUsers").onclick = loadUsers;

async function toggleUser(button) {
  const disabled = button.dataset.disabled === "true";
  if (!confirm(`\u786e\u8ba4${disabled ? "\u5c01\u7981" : "\u89e3\u5c01"}\u8be5\u7528\u6237\uff1f`)) return;
  await request(`/api/admin/users/${button.dataset.id}`, {method: "PATCH", body: JSON.stringify({disabled})});
  toast("\u7528\u6237\u72b6\u6001\u5df2\u66f4\u65b0");
  await loadUsers();
}

async function loadAnnouncements() {
  adminState.announcements = await request("/api/admin/announcements");
  $("#announcementList").innerHTML = adminState.announcements.length
    ? adminState.announcements.map(item => `
      <section class="announcement-item">
        <header><b>${esc(item.title)}</b>${item.active ? "" : ' <span class="status">\u5df2\u505c\u7528</span>'}</header>
        <p>${esc(item.body)}</p>
        <small>${date(item.updatedAt)}</small>
        <footer>
          <button class="action ann-edit" data-id="${item.id}">\u7f16\u8f91</button>
          <button class="action danger ann-delete" data-id="${item.id}">\u5220\u9664</button>
        </footer>
      </section>`).join("")
    : '<p class="muted">\u6682\u65e0\u516c\u544a</p>';
  document.querySelectorAll(".ann-edit").forEach(button => button.onclick = () => editAnnouncement(Number(button.dataset.id)));
  document.querySelectorAll(".ann-delete").forEach(button => button.onclick = () => deleteAnnouncement(Number(button.dataset.id)));
}

function editAnnouncement(id) {
  const item = adminState.announcements.find(value => value.id === id);
  if (!item) return;
  $("#announcementID").value = item.id;
  $("#announcementTitle").value = item.title;
  $("#announcementBody").value = item.body;
  $("#announcementActive").checked = item.active;
  $("#announcementFormTitle").textContent = "\u7f16\u8f91\u516c\u544a";
}

function resetAnnouncement() {
  $("#announcementForm").reset();
  $("#announcementID").value = "";
  $("#announcementActive").checked = true;
  $("#announcementFormTitle").textContent = "\u53d1\u5e03\u516c\u544a";
}

$("#cancelAnnouncement").onclick = resetAnnouncement;
$("#announcementForm").onsubmit = async event => {
  event.preventDefault();
  const id = $("#announcementID").value;
  const body = {title: $("#announcementTitle").value, body: $("#announcementBody").value, active: $("#announcementActive").checked};
  await request(id ? `/api/admin/announcements/${id}` : "/api/admin/announcements", {method: id ? "PUT" : "POST", body: JSON.stringify(body)});
  resetAnnouncement();
  toast("\u516c\u544a\u5df2\u4fdd\u5b58");
  await loadAnnouncements();
};

async function deleteAnnouncement(id) {
  if (!confirm("\u786e\u8ba4\u5220\u9664\u8fd9\u6761\u516c\u544a\uff1f")) return;
  await request(`/api/admin/announcements/${id}`, {method: "DELETE"});
  toast("\u516c\u544a\u5df2\u5220\u9664");
  await loadAnnouncements();
}

async function loadThreads() {
  adminState.threads = await request("/api/admin/chats");
  const unread = adminState.threads.reduce((sum, item) => sum + item.unread, 0);
  $("#chatCount").textContent = unread || "";
  $("#threads").innerHTML = adminState.threads.length
    ? adminState.threads.map(thread => `
      <button class="thread${thread.userId === adminState.userId ? " active" : ""}" data-user="${thread.userId}">
        ${thread.unread ? `<i>${thread.unread}</i>` : ""}
        <b>${esc(thread.email)}</b>
        <span>${esc(thread.lastMessage)}</span>
        <span>${date(thread.lastAt)}</span>
      </button>`).join("")
    : '<div class="muted" style="padding:22px">\u6682\u65e0\u5ba2\u670d\u4f1a\u8bdd</div>';
  document.querySelectorAll(".thread").forEach(button => button.onclick = () => openChat(Number(button.dataset.user)));
  if (adminState.userId) await loadMessages();
}

async function openChat(userId) {
  adminState.userId = userId;
  const thread = adminState.threads.find(item => item.userId === userId);
  $("#chatHeader").textContent = thread?.email || `\u7528\u6237 ${userId}`;
  $("#reply").disabled = false;
  $("#replyForm button").disabled = false;
  await loadMessages();
  await loadThreads();
}

async function loadMessages() {
  if (!adminState.userId) return;
  const messages = await request(`/api/admin/chats/${adminState.userId}`);
  const box = $("#messages");
  box.innerHTML = messages.map(message => `<div class="message ${message.sender}">${esc(message.body)}<small>${date(message.createdAt)}</small></div>`).join("");
  box.scrollTop = box.scrollHeight;
}

$("#replyForm").onsubmit = async event => {
  event.preventDefault();
  const body = $("#reply").value.trim();
  if (!body) return;
  await request(`/api/admin/chats/${adminState.userId}`, {method: "POST", body: JSON.stringify({body})});
  $("#reply").value = "";
  await loadMessages();
};

async function loadAudit() {
  const items = await request("/api/admin/audit");
  $("#auditRows").innerHTML = items.length
    ? items.map(item => `<tr><td>${esc(item.action)}</td><td>${esc(item.target)}</td><td>${esc(item.detail)}</td><td>${date(item.at)}</td></tr>`).join("")
    : '<tr><td colspan="4" class="muted">\u6682\u65e0\u64cd\u4f5c\u8bb0\u5f55</td></tr>';
}

async function loadSettings() {
  const data = await request("/api/admin/settings");
  const settings = data.settings;
  Object.keys(settings).forEach(key => {
    const input = $(`#${key}`);
    if (!input) return;
    if (input.type === "checkbox") input.checked = settings[key] === "true";
    else input.value = settings[key];
  });
  $("#smtpPassword").value = "";
  $("#resendApiKey").value = "";
  setConfigStatus("#smtpPasswordStatus", data.smtpPasswordConfigured ? "SMTP \u5bc6\u7801\u5df2\u914d\u7f6e" : "SMTP \u5bc6\u7801\u672a\u914d\u7f6e", data.smtpPasswordConfigured);
  setConfigStatus("#resendApiKeyStatus", data.resendApiKeyConfigured ? "Resend Key \u5df2\u914d\u7f6e" : "Resend Key \u672a\u914d\u7f6e", data.resendApiKeyConfigured);
  setConfigStatus("#turnstileStatus", data.turnstileConfigured ? "Turnstile \u5df2\u914d\u7f6e" : "Turnstile \u672a\u914d\u7f6e", data.turnstileConfigured);
  syncMailProviderUI();
}

function setConfigStatus(selector, text, ready) {
  const element = $(selector);
  element.textContent = text;
  element.classList.toggle("missing", !ready);
}

function syncMailProviderUI() {
  const provider = $("#mailProvider").value || "resend";
  $("#resendFields").classList.toggle("hidden", provider !== "resend");
  $("#smtpFields").classList.toggle("hidden", provider !== "smtp");
}

$("#settingsForm").onsubmit = async event => {
  event.preventDefault();
  const keys = ["markupCNY", "usdCnyRate", "smsmanCnyRate", "blockedCountries", "refundWindowMinutes", "refundMaxCount", "mailProvider", "smtpHost", "smtpPort", "smtpUser", "smtpFrom", "resendFrom", "contactTitle", "contactValue", "contactURL", "supportHours"];
  const body = {};
  keys.forEach(key => body[key] = $(`#${key}`).value);
  body.emailVerificationRequired = String($("#emailVerificationRequired").checked);
  if ($("#smtpPassword").value) body.smtpPassword = $("#smtpPassword").value;
  if ($("#resendApiKey").value) body.resendApiKey = $("#resendApiKey").value;
  await request("/api/admin/settings", {method: "PUT", body: JSON.stringify(body)});
  $("#settingsResult").textContent = "\u8bbe\u7f6e\u5df2\u4fdd\u5b58";
  await loadSettings();
  setTimeout(() => $("#settingsResult").textContent = "", 1800);
};

$("#mailProvider").onchange = syncMailProviderUI;

setInterval(() => {
  if (!$("#panel").classList.contains("hidden")) {
    loadThreads().catch(() => {});
    if (adminState.view === "dashboard") loadOverview().catch(() => {});
  }
}, 10000);

async function bootstrapAdmin() {
  const params = new URLSearchParams(location.search);
  const pending = params.has("session") || localStorage.getItem("adminLoginPending") === "1";
  if (pending) {
    $("#login").classList.add("hidden");
    $("#panel").classList.remove("hidden");
  }
  const attempts = pending ? 6 : 1;
  for (let index = 0; index < attempts; index += 1) {
    try {
      await request("/api/admin/overview");
      localStorage.removeItem("adminLoginPending");
      if (params.has("session")) history.replaceState(null, "", "/admin.html");
      showPanel();
      return;
    } catch (error) {
      if (!pending || index === attempts - 1) {
        localStorage.removeItem("adminLoginPending");
        $("#panel").classList.add("hidden");
        $("#login").classList.remove("hidden");
        return;
      }
      await new Promise(resolve => setTimeout(resolve, 350));
    }
  }
}

bootstrapAdmin();
