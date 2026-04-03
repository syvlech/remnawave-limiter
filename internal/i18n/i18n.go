package i18n

var current = "ru"

var translations = map[string]map[string]string{
	"ru": {
		"alert.manual.title":    "⚠️ <b>Превышение лимита устройств</b>",
		"alert.auto.title":      "🔒 <b>Подписка автоматически отключена</b>",
		"alert.user":            "👤 Пользователь",
		"alert.limit":           "📊 Лимит",
		"alert.detected_ips":    "Обнаружено",
		"alert.violations_24h":  "📈 Нарушений за 24ч",
		"alert.disabled_for":    "⏱ Отключена на",
		"alert.permanent":       "Перманентно",
		"alert.ips_header":      "📍 IP-адреса",
		"alert.node":            "нода",
		"alert.profile":         "🔗 Профиль",

		"action.drop":           "✅ Подключения сброшены",
		"action.disable":        "🔒 Подписка отключена навсегда",
		"action.disable_temp":   "🔒 Подписка временно отключена",
		"action.ignore":         "🔇 Добавлен в whitelist",
		"action.enable":         "🔓 Подписка включена",
		"action.unknown":        "❓ Неизвестное действие",
		"action.admin":          "админ",

		"button.drop":           "🔄 Сбросить подключения",
		"button.disable_forever": "🔒 Отключить навсегда",
		"button.disable_for":    "🔒 Отключить на",
		"button.ignore":         "🔇 Игнорировать",
		"button.enable":         "🔓 Включить подписку",

		"callback.no_access":    "⛔ Нет доступа",
		"callback.done":         "✅ Выполнено",
		"callback.error":        "❌ Ошибка",

		"restore.message":       "🔓 Подписка <code>%s</code> автоматически включена по таймеру",

		"duration.forever":      "навсегда",
		"duration.min":          "мин",
		"duration.hour":         "ч",
		"duration.day":          "д",

		"log.monitoring_started":  "🚀 Мониторинг запущен",
		"log.monitoring_stopped":  "Мониторинг остановлен",
		"log.limit_exceeded":      "Обнаружено превышение лимита устройств",
	},
	"en": {
		"alert.manual.title":    "⚠️ <b>Device limit exceeded</b>",
		"alert.auto.title":      "🔒 <b>Subscription automatically disabled</b>",
		"alert.user":            "👤 User",
		"alert.limit":           "📊 Limit",
		"alert.detected_ips":    "Detected",
		"alert.violations_24h":  "📈 Violations in 24h",
		"alert.disabled_for":    "⏱ Disabled for",
		"alert.permanent":       "Permanently",
		"alert.ips_header":      "📍 IP addresses",
		"alert.node":            "node",
		"alert.profile":         "🔗 Profile",

		"action.drop":           "✅ Connections dropped",
		"action.disable":        "🔒 Subscription disabled permanently",
		"action.disable_temp":   "🔒 Subscription temporarily disabled",
		"action.ignore":         "🔇 Added to whitelist",
		"action.enable":         "🔓 Subscription enabled",
		"action.unknown":        "❓ Unknown action",
		"action.admin":          "admin",

		"button.drop":           "🔄 Drop connections",
		"button.disable_forever": "🔒 Disable permanently",
		"button.disable_for":    "🔒 Disable for",
		"button.ignore":         "🔇 Ignore",
		"button.enable":         "🔓 Enable subscription",

		"callback.no_access":    "⛔ Access denied",
		"callback.done":         "✅ Done",
		"callback.error":        "❌ Error",

		"restore.message":       "🔓 Subscription <code>%s</code> automatically enabled by timer",

		"duration.forever":      "forever",
		"duration.min":          "min",
		"duration.hour":         "h",
		"duration.day":          "d",

		"log.monitoring_started":  "🚀 Monitoring started",
		"log.monitoring_stopped":  "Monitoring stopped",
		"log.limit_exceeded":      "Device limit exceeded",
	},
}

func SetLanguage(lang string) {
	if _, ok := translations[lang]; ok {
		current = lang
	}
}

func T(key string) string {
	if msg, ok := translations[current][key]; ok {
		return msg
	}
	if msg, ok := translations["en"][key]; ok {
		return msg
	}
	return key
}
