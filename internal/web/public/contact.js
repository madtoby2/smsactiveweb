fetch('/api/settings').then(response => response.json()).then(settings => {
  document.querySelector('#dynamicContactTitle').textContent = settings.contactTitle || '在线客服';
  document.querySelector('#dynamicContactValue').textContent = settings.contactValue || '请登录后使用客服聊天';
  document.querySelector('#dynamicSupportHours').textContent = settings.supportHours || '';
  const link = document.querySelector('#dynamicContactLink');
  if (settings.contactURL) {
    link.href = settings.contactURL;
    link.textContent = '打开联系方式';
  }
}).catch(() => {
  document.querySelector('#dynamicContactValue').textContent = '请登录后使用客服聊天';
});
