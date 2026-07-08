package web

import (
	"html/template"
	"log"
	"net/http"
	"path"
)

type pageView struct {
	Template    string
	Title       string
	Description string
	Robots      string
	Canonical   string
	Stylesheets []string
	Scripts     []string
}

var pageViews = map[string]pageView{
	"/api.html": {Template: "api.tmpl", Title: "API 接入 - 云码台", Description: "云码台合作 API 能力说明，包括实时目录、报价、取号、查码和取消换号。"},
	"/contact.html": {Template: "contact.tmpl", Title: "联系我们 - 云码台", Description: "联系云码台处理订单问题、商务合作和 API 接入。", Scripts: []string{"/contact.js?v=20260626b"}},
	"/cookie.html": {Template: "cookie.tmpl", Title: "Cookie 政策 - 云码台", Description: "了解云码台如何使用必要 Cookie、本地存储和安全验证技术。"},
	"/privacy.html": {Template: "privacy.tmpl", Title: "隐私政策 - 云码台", Description: "了解云码台收集、使用和保护用户信息的方式。"},
	"/terms.html": {Template: "terms.tmpl", Title: "用户协议 - 云码台", Description: "云码台用户协议，说明账户、订单、支付、取消与禁止行为等规则。"},
	"/keywords.html": {Template: "keywords.tmpl", Title: "接码关键词导航页 - 云码台", Description: "Telegram、ChatGPT、OpenAI、Google、WhatsApp 等接码关键词入口。", Stylesheets: seoStyles()},
	"/international-sms-platform.html": {Template: "international-sms-platform.tmpl", Title: "海外短信验证码平台说明 - 云码台", Description: "海外短信验证码平台使用说明，介绍国家、服务、库存和支付流程。", Stylesheets: seoStyles()},
	"/telegram-phone.html": {Template: "telegram-phone.tmpl", Title: "Telegram 手机号接码说明 - 云码台", Description: "Telegram 手机号、Telegram 接码和 Telegram 验证码接收说明。", Stylesheets: seoStyles()},
	"/telegram-register-phone.html": {Template: "telegram-register-phone.tmpl", Title: "Telegram 注册手机号说明 - 云码台", Description: "Telegram 注册手机号选择、接码流程和国家线路说明。", Stylesheets: seoStyles()},
	"/chatgpt-phone.html": {Template: "chatgpt-phone.tmpl", Title: "ChatGPT 手机号验证页 - 云码台", Description: "ChatGPT 手机号验证、OpenAI 接码和手机号选择说明。", Stylesheets: seoStyles()},
	"/chatgpt-verification-code.html": {Template: "chatgpt-verification-code.tmpl", Title: "ChatGPT 验证码接收说明 - 云码台", Description: "ChatGPT 验证码接收流程和接码注意事项。", Stylesheets: seoStyles()},
	"/openai-phone.html": {Template: "openai-phone.tmpl", Title: "OpenAI 手机号说明 - 云码台", Description: "OpenAI 手机号验证和短信验证码接收说明。", Stylesheets: seoStyles()},
	"/openai-register-phone.html": {Template: "openai-register-phone.tmpl", Title: "OpenAI 注册手机号说明 - 云码台", Description: "OpenAI 注册手机号、国家选择和接码流程说明。", Stylesheets: seoStyles()},
	"/google-phone.html": {Template: "google-phone.tmpl", Title: "Google 手机号验证说明 - 云码台", Description: "Google 手机号验证、YouTube 和 Gmail 接码相关说明。", Stylesheets: seoStyles()},
	"/gmail-phone.html": {Template: "gmail-phone.tmpl", Title: "Gmail 手机号说明 - 云码台", Description: "Gmail 注册手机号、邮箱验证和短信验证码接收说明。", Stylesheets: seoStyles()},
	"/facebook-phone.html": {Template: "facebook-phone.tmpl", Title: "Facebook 手机号说明 - 云码台", Description: "Facebook 注册、登录和风控验证手机号说明。", Stylesheets: seoStyles()},
	"/twitter-phone.html": {Template: "twitter-phone.tmpl", Title: "Twitter/X 手机号说明 - 云码台", Description: "Twitter/X 注册、登录和短信验证手机号说明。", Stylesheets: seoStyles()},
	"/whatsapp-phone.html": {Template: "whatsapp-phone.tmpl", Title: "WhatsApp 手机号说明 - 云码台", Description: "WhatsApp 注册、登录和短信验证码接收说明。", Stylesheets: seoStyles()},
}

func seoStyles() []string {
	return []string{"/app.css?v=20260627c", "/footer.css?v=20260702a"}
}

func (p pageView) withDefaults(requestPath string) pageView {
	if p.Robots == "" {
		p.Robots = "index,follow"
	}
	if p.Canonical == "" {
		p.Canonical = "https://yunmatai.xyz" + requestPath
	}
	if len(p.Stylesheets) == 0 {
		p.Stylesheets = []string{"/app.css?v=20260626b", "/footer.css?v=20260626b"}
	}
	return p
}

func (s *Server) page(w http.ResponseWriter, r *http.Request) {
	view, ok := pageViews[r.URL.Path]
	if !ok {
		http.NotFound(w, r)
		return
	}
	tpl, err := template.ParseFS(assets, "templates/layout.html", path.Join("templates/pages", view.Template))
	if err != nil {
		fail(w, http.StatusInternalServerError, err)
		return
	}
	w.Header().Set("content-type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store, no-cache, must-revalidate")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	if err = tpl.ExecuteTemplate(w, "layout", view.withDefaults(r.URL.Path)); err != nil {
		log.Printf("render %s failed: %v", r.URL.Path, err)
	}
}
