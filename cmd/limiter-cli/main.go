package main

import (
	"bufio"
	"fmt"
	"net"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strings"
)

const (
	ColorRed     = "\033[0;31m"
	ColorGreen   = "\033[0;32m"
	ColorYellow  = "\033[1;33m"
	ColorBlue    = "\033[0;34m"
	ColorMagenta = "\033[0;35m"
	ColorCyan    = "\033[0;36m"
	ColorNC      = "\033[0m"
)

const (
	JailName     = "remnawave-limiter"
	ViolationLog = "/var/log/remnawave-limiter/access-limiter.log"
	RemnawaveLog = "/var/log/remnanode/access.log"
	ServiceName  = "remnawave-limiter"
)

func main() {
	if len(os.Args) < 2 {
		printHelp()
		os.Exit(1)
	}

	command := os.Args[1]

	switch command {
	case "status":
		showStatus()
	case "violations":
		lines := 20
		if len(os.Args) > 2 && os.Args[2] == "-n" && len(os.Args) > 3 {
			if _, err := fmt.Sscanf(os.Args[3], "%d", &lines); err != nil || lines <= 0 {
				fmt.Printf("%s❌ Неверное значение для -n: %s (должно быть > 0)%s\n", ColorRed, os.Args[3], ColorNC)
				os.Exit(1)
			}
		}
		showViolations(lines)
	case "banned":
		listBanned()
	case "unban":
		if len(os.Args) < 3 {
			fmt.Printf("%s❌ Укажите IP для разбана%s\n", ColorRed, ColorNC)
			os.Exit(1)
		}
		unbanIP(os.Args[2])
	case "unban-all":
		unbanAll()
	case "active":
		showActiveConnections()
	case "clear":
		clearLogs()
	case "logs":
		follow := false
		lines := 50
		for i := 2; i < len(os.Args); i++ {
			if os.Args[i] == "-f" || os.Args[i] == "--follow" {
				follow = true
			}
			if os.Args[i] == "-n" && i+1 < len(os.Args) {
				if _, err := fmt.Sscanf(os.Args[i+1], "%d", &lines); err != nil || lines <= 0 {
					fmt.Printf("%s❌ Неверное значение для -n: %s (должно быть > 0)%s\n", ColorRed, os.Args[i+1], ColorNC)
					os.Exit(1)
				}
			}
		}
		showLogs(follow, lines)
	default:
		printHelp()
	}
}

func printHelp() {
	fmt.Println("Remnawave IP Limiter CLI")
	fmt.Println("\nИспользование:")
	fmt.Println("  limiter-cli status                    # Показать статус системы")
	fmt.Println("  limiter-cli violations                # Последние 20 нарушений")
	fmt.Println("  limiter-cli violations -n 50          # Последние 50 нарушений")
	fmt.Println("  limiter-cli banned                    # Список забаненных IP")
	fmt.Println("  limiter-cli unban 1.2.3.4             # Разбанить IP")
	fmt.Println("  limiter-cli unban-all                 # Разбанить все IP")
	fmt.Println("  limiter-cli active                    # Активные подключения")
	fmt.Println("  limiter-cli logs                      # Последние 50 строк логов")
	fmt.Println("  limiter-cli logs -f                   # Следить за логами (Ctrl+C для выхода)")
	fmt.Println("  limiter-cli clear                     # Очистить все логи")
}

func runCommand(name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	output, err := cmd.CombinedOutput()
	return string(output), err
}

func showStatus() {
	fmt.Printf("%s╔════════════════════════════════════════════════════════╗%s\n", ColorBlue, ColorNC)
	fmt.Printf("%s║         Remnawave IP Limiter - Статус системы          ║%s\n", ColorBlue, ColorNC)
	fmt.Printf("%s╚════════════════════════════════════════════════════════╝%s\n\n", ColorBlue, ColorNC)

	output, _ := runCommand("systemctl", "is-active", ServiceName)
	serviceStatus := strings.TrimSpace(output)
	statusColor := ColorGreen
	if serviceStatus != "active" {
		statusColor = ColorRed
	}
	fmt.Printf("📊 Сервис remnawave-limiter: %s%s%s\n", statusColor, serviceStatus, ColorNC)

	output, _ = runCommand("systemctl", "is-active", "fail2ban")
	f2bStatus := strings.TrimSpace(output)
	f2bColor := ColorGreen
	if f2bStatus != "active" {
		f2bColor = ColorRed
	}
	fmt.Printf("🔒 Fail2ban: %s%s%s\n\n", f2bColor, f2bStatus, ColorNC)

	output, err := runCommand("fail2ban-client", "status", JailName)
	if err == nil {
		fmt.Printf("%sСтатус jail '%s':%s\n", ColorCyan, JailName, ColorNC)
		fmt.Println(output)
	} else {
		fmt.Printf("%s❌ Не удалось получить статус jail%s\n", ColorRed, ColorNC)
	}
}

func listBanned() {
	output, err := runCommand("fail2ban-client", "status", JailName)
	if err != nil {
		fmt.Printf("%s❌ Ошибка получения списка банов%s\n", ColorRed, ColorNC)
		return
	}

	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Banned IP list") {
			parts := strings.Split(line, ":")
			if len(parts) < 2 {
				continue
			}
			ips := strings.TrimSpace(parts[1])
			if ips == "" {
				fmt.Printf("%s✅ Нет забаненных IP%s\n", ColorGreen, ColorNC)
			} else {
				fmt.Printf("%s🚫 Забаненные IP:%s\n", ColorYellow, ColorNC)
				for _, ip := range strings.Fields(ips) {
					fmt.Printf("   • %s%s%s\n", ColorRed, ip, ColorNC)
				}
			}
			return
		}
	}

	fmt.Printf("%s✅ Нет забаненных IP%s\n", ColorGreen, ColorNC)
}

func unbanIP(ip string) {
	if net.ParseIP(ip) == nil {
		fmt.Printf("%s❌ Неверный формат IP адреса: %s%s\n", ColorRed, ip, ColorNC)
		os.Exit(1)
	}

	fmt.Printf("🔓 Разбан %s%s%s...\n", ColorCyan, ip, ColorNC)

	_, err := runCommand("fail2ban-client", "set", JailName, "unbanip", ip)
	if err != nil {
		fmt.Printf("%s❌ Ошибка разбана: %v%s\n", ColorRed, err, ColorNC)
	} else {
		fmt.Printf("%s✅ IP %s разбанен%s\n", ColorGreen, ip, ColorNC)
	}
}

func unbanAll() {
	output, err := runCommand("fail2ban-client", "status", JailName)
	if err != nil {
		fmt.Printf("%s❌ Ошибка получения списка банов%s\n", ColorRed, ColorNC)
		return
	}

	var bannedIPs []string
	lines := strings.Split(output, "\n")
	for _, line := range lines {
		if strings.Contains(line, "Banned IP list") {
			parts := strings.Split(line, ":")
			if len(parts) < 2 {
				continue
			}
			ips := strings.TrimSpace(parts[1])
			if ips != "" {
				bannedIPs = strings.Fields(ips)
			}
			break
		}
	}

	if len(bannedIPs) == 0 {
		fmt.Printf("%s✅ Нет забаненных IP%s\n", ColorGreen, ColorNC)
		return
	}

	fmt.Printf("🔓 Разбан %d IP адресов...\n", len(bannedIPs))

	for _, ip := range bannedIPs {
		runCommand("fail2ban-client", "set", JailName, "unbanip", ip)
		fmt.Printf("  ✓ %s\n", ip)
	}

	fmt.Printf("%s✅ Все IP разбанены%s\n", ColorGreen, ColorNC)
}

func showViolations(tail int) {
	file, err := os.Open(ViolationLog)
	if err != nil {
		fmt.Printf("%s⚠️  Лог нарушений пуст%s\n", ColorYellow, ColorNC)
		return
	}
	defer file.Close()

	fmt.Printf("%s📋 Последние %d нарушений:%s\n\n", ColorBlue, tail, ColorNC)

	ring := make([]string, tail)
	ringIdx := 0
	total := 0

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		ring[ringIdx%tail] = scanner.Text()
		ringIdx++
		total++
	}

	if total == 0 {
		fmt.Printf("%s⚠️  Нет нарушений%s\n", ColorYellow, ColorNC)
		return
	}

	count := total
	if count > tail {
		count = tail
	}
	start := (ringIdx - count) % tail
	if start < 0 {
		start += tail
	}

	re := regexp.MustCompile(`(\d{4}/\d{2}/\d{2} \d{2}:\d{2}:\d{2}).*Email = (\S+).*SRC = (\S+)`)
	for i := 0; i < count; i++ {
		line := ring[(start+i)%tail]
		match := re.FindStringSubmatch(line)
		if len(match) >= 4 {
			timestamp := match[1]
			email := match[2]
			ip := match[3]
			fmt.Printf("%s%s%s │ %s%s%s │ %s%s%s\n",
				ColorYellow, timestamp, ColorNC,
				ColorCyan, email, ColorNC,
				ColorRed, ip, ColorNC)
		} else {
			fmt.Println(line)
		}
	}
}

func showActiveConnections() {
	file, err := os.Open(RemnawaveLog)
	if err != nil {
		fmt.Printf("%s❌ Лог Remnawave не найден: %s%s\n", ColorRed, RemnawaveLog, ColorNC)
		return
	}
	defer file.Close()

	emailIPs := make(map[string]map[string]bool)
	re := regexp.MustCompile(`from\s+(\d{1,3}\.\d{1,3}\.\d{1,3}\.\d{1,3}):\d+\s+accepted.*?email:\s*(\S+)`)

	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		match := re.FindStringSubmatch(scanner.Text())
		if len(match) >= 3 {
			ip := match[1]
			email := match[2]
			if emailIPs[email] == nil {
				emailIPs[email] = make(map[string]bool)
			}
			emailIPs[email][ip] = true
		}
	}

	if len(emailIPs) == 0 {
		fmt.Printf("%s⚠️  Нет активных подключений в логе%s\n", ColorYellow, ColorNC)
		return
	}

	fmt.Printf("%s╔════════════════════════════════════════════════════════╗%s\n", ColorBlue, ColorNC)
	fmt.Printf("%s║            Активные подключения (текущий лог)          ║%s\n", ColorBlue, ColorNC)
	fmt.Printf("%s╚════════════════════════════════════════════════════════╝%s\n\n", ColorBlue, ColorNC)

	type emailIPCount struct {
		email string
		count int
		ips   []string
	}
	var sorted []emailIPCount
	for email, ips := range emailIPs {
		var ipList []string
		for ip := range ips {
			ipList = append(ipList, ip)
		}
		sort.Strings(ipList)
		sorted = append(sorted, emailIPCount{email, len(ipList), ipList})
	}
	sort.Slice(sorted, func(i, j int) bool {
		return sorted[i].count > sorted[j].count
	})

	for _, item := range sorted {
		color := ColorGreen
		if item.count > 1 {
			color = ColorRed
		}
		fmt.Printf("%s📧 %s (%d IP)%s\n", color, item.email, item.count, ColorNC)
		for _, ip := range item.ips {
			fmt.Printf("   └─ %s%s%s\n", ColorCyan, ip, ColorNC)
		}
		fmt.Println()
	}
}

func clearLogs() {
	fmt.Printf("%s⚠️  Это удалит все логи нарушений и Remnawave access log!%s\n", ColorYellow, ColorNC)
	fmt.Print("Продолжить? (yes/no): ")

	reader := bufio.NewReader(os.Stdin)
	response, _ := reader.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))

	if response != "yes" {
		fmt.Printf("%sОтменено%s\n", ColorBlue, ColorNC)
		return
	}

	fmt.Printf("\n%sОстановка сервиса...%s\n", ColorBlue, ColorNC)
	if _, err := runCommand("systemctl", "stop", ServiceName); err != nil {
		fmt.Printf("%s⚠️  Ошибка остановки сервиса: %v%s\n", ColorYellow, err, ColorNC)
	}

	if err := os.Truncate(ViolationLog, 0); err == nil {
		fmt.Printf("%s✅ Лог нарушений очищен%s\n", ColorGreen, ColorNC)
	} else {
		fmt.Printf("%s❌ Ошибка очистки лога нарушений: %v%s\n", ColorRed, err, ColorNC)
	}

	if err := os.Truncate(RemnawaveLog, 0); err == nil {
		fmt.Printf("%s✅ Access лог очищен%s\n", ColorGreen, ColorNC)
	} else {
		fmt.Printf("%s❌ Ошибка очистки access лога: %v%s\n", ColorRed, err, ColorNC)
	}

	fmt.Printf("\n%sЗапуск сервиса...%s\n", ColorBlue, ColorNC)
	if _, err := runCommand("systemctl", "start", ServiceName); err != nil {
		fmt.Printf("%s❌ Ошибка запуска сервиса: %v%s\n", ColorRed, err, ColorNC)
	} else {
		fmt.Printf("%s✅ Сервис запущен%s\n", ColorGreen, ColorNC)
	}
}

func showLogs(follow bool, lines int) {
	args := []string{"-u", ServiceName, "--no-pager", "-n", fmt.Sprintf("%d", lines)}

	if follow {
		args = append(args, "-f")
	}

	cmd := exec.Command("journalctl", args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	cmd.Stdin = os.Stdin

	if err := cmd.Run(); err != nil {
		fmt.Printf("%s❌ Ошибка выполнения journalctl: %v%s\n", ColorRed, err, ColorNC)
	}
}
