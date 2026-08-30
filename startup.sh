#!/bin/sh

# ================================================================
# GAPI 服务守护脚本
#   - 自动下载 / 热更新 gapi 二进制
#   - 进程崩溃后指数退避重启
#   - 端口冲突自动清理 + 健康检查自动拉起
# ================================================================

# ============ 配置区 ============
PLUGIN_DIR="/plugins/data/apilab"
BINARY_NAME="gapi"
TMP_NAME="gapi.tmp"
TMP_ARCHIVE="gapi.tmp.tar.gz"
EXTRACT_DIR="$PLUGIN_DIR/.extract"
LOG_FILE="$PLUGIN_DIR/logs/gapi.log"
PID_FILE="$PLUGIN_DIR/gapi.pid"
DOWNLOAD_URL="https://github.com/YellCatt/apilab/releases/download/dev-latest/gapi_linux_mipsle.tar.gz"

# 服务运行相关
CONFIG_DIR="$PLUGIN_DIR/config"
CONFIG_FILE="$CONFIG_DIR/config.yaml"
DATA_DIR="$PLUGIN_DIR"
SERVER_PORT=8084
HEALTH_PATH="/health"

MAX_RETRY=20
RESTART_DELAY=5
MAX_RESTART_DELAY=300
UPDATE_INTERVAL=10800         # 172800 秒 = 48 小时 14400 秒 = 4小时 86400 秒 = 24 小时  3小时 = 10800 秒
GRACEFUL_SHUTDOWN_TIMEOUT=10

# 下载超时配置
CONNECT_TIMEOUT=120
MAX_DOWNLOAD_TIME=1200

# 健康检查配置（1 开启 / 0 关闭）
ENABLE_HEALTH_CHECK=1
HEALTH_CHECK_TIMEOUT=5
HEALTH_FAIL_THRESHOLD=6       # 每轮循环间隔 10 秒，6 次约 1 分钟无响应则重启

# 预创建目录，确保早期日志能写入
mkdir -p "$PLUGIN_DIR" 2>/dev/null
mkdir -p "$PLUGIN_DIR/logs" 2>/dev/null

# ============ 全局状态 ============
CHILD_PID=""
NEED_UPDATE=0
RUNNING=1
CURRENT_DELAY=$RESTART_DELAY
HEALTH_FAIL_COUNT=0

# ============ 日志函数 ============
log() {
    echo "[$(date '+%Y-%m-%d %H:%M:%S')] $1" >> "$LOG_FILE"
}

log_info()  { log "【信息】$1"; }
log_ok()    { log "【成功】✓ $1"; }
log_warn()  { log "【警告】⚠ $1"; }
log_error() { log "【错误】✗ $1"; }
log_step()  { log "【步骤】$1"; }

# ============ 清理函数 ============
cleanup() {
    log_info "收到退出信号，开始清理..."
    RUNNING=0

    if [ -n "$CHILD_PID" ]; then
        kill "$CHILD_PID" 2>/dev/null
        sleep 1
        kill -9 "$CHILD_PID" 2>/dev/null
        wait "$CHILD_PID" 2>/dev/null
    fi

    [ -f "$PID_FILE" ] && rm -f "$PID_FILE"
    [ -f "$PLUGIN_DIR/$TMP_NAME" ] && rm -f "$PLUGIN_DIR/$TMP_NAME"
    [ -f "$PLUGIN_DIR/$TMP_ARCHIVE" ] && rm -f "$PLUGIN_DIR/$TMP_ARCHIVE"
    [ -d "$EXTRACT_DIR" ] && rm -rf "$EXTRACT_DIR"
    log_ok "清理完成，脚本退出"
    exit 0
}

trap 'cleanup' INT TERM

# ============ 启动前强制清理残留进程 ============
killall -9 gapi 2>/dev/null
rm -f "$PID_FILE"
log_info "已清理可能残留的 gapi 进程和 PID 文件"

# ============ 防重复启动 ============
log "========================================"
log_info "gapi 守护脚本启动"
log_info "当前工作目录: $(pwd)"
log_info "插件目录: $PLUGIN_DIR"
log_info "下载地址: $DOWNLOAD_URL"
log_info "最大下载重试: $MAX_RETRY 次"
log_info "更新检查间隔: ${UPDATE_INTERVAL} 秒"
log_info "连接超时: ${CONNECT_TIMEOUT} 秒"
log_info "单次下载最大耗时: ${MAX_DOWNLOAD_TIME} 秒"

if [ -f "$PID_FILE" ]; then
    OLD_PID=$(cat "$PID_FILE" 2>/dev/null)
    if [ -n "$OLD_PID" ] && kill -0 "$OLD_PID" 2>/dev/null; then
        log_error "检测到已有实例在运行 (PID: $OLD_PID)，请勿重复启动"
        exit 1
    else
        log_warn "发现残留 PID 文件，但对应进程已不存在，继续启动"
        rm -f "$PID_FILE"
    fi
fi

echo $$ > "$PID_FILE"
log_ok "PID 文件已写入: $PID_FILE (当前 PID: $$)"

# ============ 等待网络就绪 ============
log_step "等待网络就绪..."
NETWORK_WAIT=0
while true; do
    if ping -c 1 -W 3 8.8.8.8 > /dev/null 2>&1; then
        log_ok "网络已就绪 (累计等待 ${NETWORK_WAIT} 秒)"
        break
    fi

    NETWORK_WAIT=$((NETWORK_WAIT + 5))
    if [ $((NETWORK_WAIT % 15)) -eq 0 ]; then
        log_warn "网络未就绪，已等待 ${NETWORK_WAIT} 秒，继续等待..."
    fi
    sleep 5
done
log "========================================"

# ============ 检查并创建插件目录 ============
log_step "检查插件目录..."
if [ ! -d "$PLUGIN_DIR" ]; then
    log_info "目录不存在，正在创建: $PLUGIN_DIR"
    if mkdir -p "$PLUGIN_DIR"; then
        log_ok "目录创建成功"
    else
        log_error "目录创建失败，退出"
        exit 1
    fi
else
    log_ok "目录已存在: $PLUGIN_DIR"
fi

# 日志目录（守护日志 + gapi 自身 info/warn/error 日志）
if [ ! -d "$PLUGIN_DIR/logs" ]; then
    mkdir -p "$PLUGIN_DIR/logs" && log_ok "日志目录已创建: $PLUGIN_DIR/logs"
fi

# ============ 进入插件目录 ============
cd "$PLUGIN_DIR" || {
    log_error "进入目录失败: $PLUGIN_DIR"
    exit 1
}

# ============ 配置文件初始化 ============
# gapi 启动时从当前目录读取 config/config.yaml，缺失时程序会自建默认配置。
# 这里提前写入默认配置，保证端口、数据库路径、日志路径符合插件目录布局；
# 已存在配置时一律保留，热更新不会覆盖用户配置。
ensure_config() {
    if [ ! -d "$CONFIG_DIR" ]; then
        if ! mkdir -p "$CONFIG_DIR"; then
            log_error "配置目录创建失败: $CONFIG_DIR"
            return 1
        fi
        log_ok "配置目录已创建: $CONFIG_DIR"
    fi

    if [ ! -f "$CONFIG_FILE" ]; then
        log_info "配置文件不存在，写入默认配置: $CONFIG_FILE"
        cat > "$CONFIG_FILE" <<EOF
server:
  port: $SERVER_PORT

database:
  path: $DATA_DIR/data.db

log:
  path: $PLUGIN_DIR/logs
  level: info

collector:
  url: http://localhost:8086/api/traces/report
  batch_size: 1000
  flush_interval: 30s
EOF
        if [ $? -eq 0 ]; then
            log_ok "默认配置写入完成"
        else
            log_error "默认配置写入失败"
            return 1
        fi
    else
        log_ok "配置文件已存在，保留现有配置: $CONFIG_FILE"
    fi

    # 解析实际监听端口，供端口冲突清理与健康检查使用
    CFG_PORT=$(grep -E '^[[:space:]]*port:' "$CONFIG_FILE" 2>/dev/null | head -n 1 | tr -cd '0-9')
    if [ -n "$CFG_PORT" ]; then
        SERVER_PORT=$CFG_PORT
    fi
    log_info "服务监听端口: $SERVER_PORT"

    return 0
}

# ============ 端口占用清理 ============
# gapi 是 HTTP 服务，若端口被残留进程占用会直接启动失败，启动前先尝试释放。
kill_port_process() {
    local port=$1
    local pids=""

    if command -v fuser > /dev/null 2>&1; then
        pids=$(fuser -n tcp "$port" 2>/dev/null | tr -d ' ')
    elif command -v ss > /dev/null 2>&1; then
        pids=$(ss -lntp 2>/dev/null | grep ":$port " | grep -o 'pid=[0-9]*' | cut -d= -f2 | sort -u | tr '\n' ' ')
    elif command -v netstat > /dev/null 2>&1; then
        pids=$(netstat -lntp 2>/dev/null | grep ":$port " | grep -o '[0-9]*/' | cut -d/ -f1 | sort -u | tr '\n' ' ')
    fi

    [ -z "$pids" ] && return 0

    for pid in $pids; do
        [ "$pid" = "$$" ] && continue
        [ ! -d "/proc/$pid" ] && continue
        log_warn "端口 $port 被进程 $pid 占用，尝试终止"
        kill "$pid" 2>/dev/null
        sleep 1
        [ -d "/proc/$pid" ] && kill -9 "$pid" 2>/dev/null
    done
    sleep 1
    return 0
}

# ============ 解压函数 ============
# 将下载的 tar.gz 解压到临时目录，并从中找出可执行文件，移动到 $TMP_NAME
extract_binary() {
    log_step "开始解压: $TMP_ARCHIVE"

    if ! command -v tar > /dev/null 2>&1; then
        log_error "系统缺少 tar 命令，无法解压"
        return 1
    fi

    [ -d "$EXTRACT_DIR" ] && rm -rf "$EXTRACT_DIR"
    mkdir -p "$EXTRACT_DIR" || {
        log_error "创建解压目录失败: $EXTRACT_DIR"
        return 1
    }

    if ! tar -xzf "$TMP_ARCHIVE" -C "$EXTRACT_DIR" 2>> "$LOG_FILE"; then
        log_error "解压失败，文件可能已损坏"
        rm -rf "$EXTRACT_DIR"
        rm -f "$TMP_ARCHIVE"
        return 1
    fi
    log_ok "解压成功，目标目录: $EXTRACT_DIR"

    # 优先查找与目标名称一致的文件
    FOUND_BIN=$(find "$EXTRACT_DIR" -type f -name "$BINARY_NAME" | head -n 1)

    # 退而求其次：查找任意可执行文件
    if [ -z "$FOUND_BIN" ]; then
        FOUND_BIN=$(find "$EXTRACT_DIR" -type f -perm -u+x | head -n 1)
    fi

    # 最后兜底：目录内只有一个普通文件时，直接取用
    if [ -z "$FOUND_BIN" ]; then
        FILE_COUNT=$(find "$EXTRACT_DIR" -type f | wc -l)
        if [ "$FILE_COUNT" -eq 1 ]; then
            FOUND_BIN=$(find "$EXTRACT_DIR" -type f | head -n 1)
        fi
    fi

    if [ -z "$FOUND_BIN" ] || [ ! -s "$FOUND_BIN" ]; then
        log_error "未在解压目录中找到有效的可执行文件"
        rm -rf "$EXTRACT_DIR"
        rm -f "$TMP_ARCHIVE"
        return 1
    fi

    rm -f "$TMP_NAME"
    mv "$FOUND_BIN" "$TMP_NAME" || {
        log_error "移动解压出的可执行文件失败"
        rm -rf "$EXTRACT_DIR"
        rm -f "$TMP_ARCHIVE"
        return 1
    }

    chmod +x "$TMP_NAME"
    size=$(ls -lh "$TMP_NAME" | awk '{print $5}')
    log_ok "已提取可执行文件: $(basename "$FOUND_BIN") -> $TMP_NAME (大小: $size) 已添加执行权限"

    rm -rf "$EXTRACT_DIR"
    rm -f "$TMP_ARCHIVE"
    return 0
}

# ============ 下载函数 ============
download_binary() {
    log_step "尝试从 GitHub 下载最新版本..."

    # ==========杀掉后台正在进行的相同下载 curl 任务==========
    log_info "检查是否存在残留的旧下载 curl 进程..."
    OLD_CURL_PIDS=$(ps | grep "$DOWNLOAD_URL" | grep curl | grep -v grep | awk '{print $1}')
    if [ -n "$OLD_CURL_PIDS" ]; then
        log_warn "发现残留下载进程: $OLD_CURL_PIDS，准备终止"
        for pid in $OLD_CURL_PIDS; do
            kill "$pid" 2>/dev/null
            sleep 0.5
            kill -9 "$pid" 2>/dev/null
        done
        log_info "旧下载进程已清理"
    fi
    # ========================================================

    [ -f "$TMP_ARCHIVE" ] && rm -f "$TMP_ARCHIVE"
    [ -f "$TMP_NAME" ] && rm -f "$TMP_NAME"

    retry=0
    while [ "$retry" -lt "$MAX_RETRY" ]; do
        retry=$((retry + 1))
        log_info "第 $retry / $MAX_RETRY 次下载尝试 (连接超时 ${CONNECT_TIMEOUT}s, 最大耗时 ${MAX_DOWNLOAD_TIME}s)..."
        curl -L -k --connect-timeout "$CONNECT_TIMEOUT" --max-time "$MAX_DOWNLOAD_TIME" -o "$TMP_ARCHIVE" "$DOWNLOAD_URL"
        curl_exit=$?
        if [ "$curl_exit" -eq 0 ] && [ -f "$TMP_ARCHIVE" ] && [ -s "$TMP_ARCHIVE" ]; then
            size=$(ls -lh "$TMP_ARCHIVE" | awk '{print $5}')
            log_ok "下载成功，压缩包大小: $size"

            # 校验是否为有效的 gzip 包
            if ! tar -tzf "$TMP_ARCHIVE" > /dev/null 2>&1; then
                log_error "下载的文件不是有效的 tar.gz 包，丢弃"
                rm -f "$TMP_ARCHIVE"
            elif extract_binary; then
                return 0
            else
                log_error "解压失败，准备重试"
                rm -f "$TMP_ARCHIVE"
                rm -f "$TMP_NAME"
            fi
        else
            log_error "下载失败 (curl 退出码: $curl_exit)"
            [ -f "$TMP_ARCHIVE" ] && rm -f "$TMP_ARCHIVE"
        fi

        if [ "$retry" -lt "$MAX_RETRY" ]; then
            log_info "等待 10 秒后重试..."
            sleep 10
        fi
    done
    log_error "已达到最大重试次数 ($MAX_RETRY)，下载失败"
    return 1
}

# ============ 程序控制函数 ============
start_program() {
    if [ ! -f "$BINARY_NAME" ]; then
        log_error "二进制文件不存在，无法启动"
        return 1
    fi

    ensure_config || return 1
    kill_port_process "$SERVER_PORT"

    "./$BINARY_NAME" >> "$LOG_FILE" 2>&1 &
    CHILD_PID=$!

    # 给服务一点时间判断是否正常起来（端口是否成功监听）
    sleep 2
    if [ ! -d "/proc/$CHILD_PID" ]; then
        log_error "程序启动后立即退出，启动失败"
        CHILD_PID=""
        return 1
    fi

    HEALTH_FAIL_COUNT=0
    log_ok "程序已启动 (PID: $CHILD_PID, 端口: $SERVER_PORT)"
    return 0
}

stop_program() {
    if [ -z "$CHILD_PID" ] || [ ! -d "/proc/$CHILD_PID" ]; then
        CHILD_PID=""
        return 0
    fi

    log_info "正在停止程序 (PID: $CHILD_PID)..."
    kill "$CHILD_PID" 2>/dev/null

    count=0
    while [ -d "/proc/$CHILD_PID" ] && [ "$count" -lt "$GRACEFUL_SHUTDOWN_TIMEOUT" ]; do
        sleep 1
        count=$((count + 1))
    done

    if [ -d "/proc/$CHILD_PID" ]; then
        log_warn "程序未在 ${GRACEFUL_SHUTDOWN_TIMEOUT} 秒内退出，强制终止"
        kill -9 "$CHILD_PID" 2>/dev/null
        sleep 1
    fi

    wait "$CHILD_PID" 2>/dev/null
    stop_exit=$?
    CHILD_PID=""
    return $stop_exit
}

# ============ 健康检查（进程存活但接口无响应时自动拉起）============
check_health() {
    [ "$ENABLE_HEALTH_CHECK" -eq 1 ] || return 0
    command -v curl > /dev/null 2>&1 || return 0

    CODE=$(curl -s -m "$HEALTH_CHECK_TIMEOUT" -o /dev/null -w '%{http_code}' "http://127.0.0.1:${SERVER_PORT}${HEALTH_PATH}" 2>/dev/null)
    case "$CODE" in
        2??) HEALTH_FAIL_COUNT=0; return 0 ;;
    esac

    HEALTH_FAIL_COUNT=$((HEALTH_FAIL_COUNT + 1))
    log_warn "健康检查失败 ($HEALTH_FAIL_COUNT/$HEALTH_FAIL_THRESHOLD)，HTTP 状态码: ${CODE:-无响应}"

    if [ "$HEALTH_FAIL_COUNT" -ge "$HEALTH_FAIL_THRESHOLD" ]; then
        log_error "服务连续无响应，准备重启"
        HEALTH_FAIL_COUNT=0
        return 1
    fi
    return 0
}

# ============ 将秒数转换为人类可读的时长 ============
format_duration() {
    local total=$1
    local days=$((total / 86400))
    local hours=$(((total % 86400) / 3600))
    local mins=$(((total % 3600) / 60))
    local secs=$((total % 60))
    local result=""

    [ $days -gt 0 ] && result="${result}${days}天"
    [ $hours -gt 0 ] && result="${result}${hours}小时"
    [ $mins -gt 0 ] && result="${result}${mins}分"
    [ $secs -gt 0 ] || [ -z "$result" ] && result="${result}${secs}秒"

    echo "$result"
}

# ============ 时间戳转人类可读时间（兼容 GNU/BusyBox） ============
format_timestamp() {
    local ts=$1
    local fmt
    # 尝试 GNU date
    fmt=$(date -d "@$ts" '+%Y-%m-%d %H:%M:%S' 2>/dev/null)
    if [ -n "$fmt" ]; then
        echo "$fmt"
        return
    fi
    # 尝试 BSD date (部分嵌入式系统)
    fmt=$(date -r "$ts" '+%Y-%m-%d %H:%M:%S' 2>/dev/null)
    if [ -n "$fmt" ]; then
        echo "$fmt"
        return
    fi
    # 回退
    echo "时间戳 $ts"
}

# ============ 打印本次检查时间与下次预计检查时间 ============
log_update_schedule() {
    local check_ts=$1
    local next_ts=$((check_ts + UPDATE_INTERVAL))
    log_info "本次更新检查时间: $(format_timestamp "$check_ts")"
    log_info "下次更新预计检查时间: $(format_timestamp "$next_ts") (间隔 $(format_duration $UPDATE_INTERVAL))"
}

# ============ 更新检查函数（定时直接下载，不调用 GitHub API） ============
check_and_update() {
    now=$(date +%s)

    last_check=0
    if [ -f "$PLUGIN_DIR/.last_update_check" ]; then
        last_check=$(cat "$PLUGIN_DIR/.last_update_check" | cut -d'|' -f1)
    fi

    elapsed=$((now - last_check))
    if [ "$elapsed" -lt "$UPDATE_INTERVAL" ]; then
        return 0
    fi

    log_step "距离上次更新已 $(format_duration $elapsed)（${elapsed}秒），开始下载最新版本..."

    # 先下载并解压，只有成功后才记录检查时间
    if download_binary; then
        # 成功，记录实际检查时间 + 下次下载时间
        next_ts=$((now + UPDATE_INTERVAL))
        echo "$now|$(date '+%Y-%m-%d %H:%M:%S %Z (UTC%z)')|$(format_timestamp "$next_ts")" > "$PLUGIN_DIR/.last_update_check"
        log_update_schedule "$now"

        # 如果当前有旧版本，比较文件内容是否相同
        if [ -f "$BINARY_NAME" ]; then
            if cmp -s "$TMP_NAME" "$BINARY_NAME"; then
                log_info "下载的文件与当前版本一致，无需替换"
                rm -f "$TMP_NAME"
                return 0
            fi
            log_info "下载的文件与当前版本不同，准备替换"
        else
            log_info "当前无旧版本，直接启用新版本"
        fi

        NEED_UPDATE=1
        return 0
    else
        log_warn "下载或解压失败，继续使用当前版本"
        # 不更新时间戳，下次进入 check_and_update 时 elapsed 仍大于间隔，会继续尝试
        rm -f "$TMP_NAME"
        return 1
    fi
}

# ============ 主守护循环 ============
main_loop() {
    # ========== 启动时：优先启动本地版本，避免下载阻塞 ==========
    if ! start_program; then
        log_warn "本地版本不存在或启动失败，尝试下载..."
        if download_binary; then
            mv "$TMP_NAME" "$BINARY_NAME"
            if ! start_program; then
                log_error "程序启动失败，守护循环终止"
                return 1
            fi
        else
            log_error "下载失败且无本地版本，无法启动"
            return 1
        fi
    fi

    CURRENT_DELAY=$RESTART_DELAY

    # 标记首次检查：程序启动后立即后台下载对比
    FIRST_CHECK=1

    while [ "$RUNNING" -eq 1 ]; do
        if [ -n "$CHILD_PID" ] && [ -d "/proc/$CHILD_PID" ]; then
            # 程序正常运行中 —— 检查更新
            if [ "$FIRST_CHECK" -eq 1 ]; then
                log_step "程序已启动，立即后台检查新版本..."
                FIRST_CHECK=0
                if download_binary; then
                    # 首次检查成功，记录实际检查时间 + 下次下载时间
                    now_ts=$(date +%s)
                    next_ts=$((now_ts + UPDATE_INTERVAL))
                    echo "$now_ts|$(date '+%Y-%m-%d %H:%M:%S %Z (UTC%z)')|$(format_timestamp "$next_ts")" > "$PLUGIN_DIR/.last_update_check"
                    log_update_schedule "$now_ts"

                    if [ -f "$BINARY_NAME" ] && cmp -s "$TMP_NAME" "$BINARY_NAME"; then
                        log_info "当前已是最新版本，无需替换"
                        rm -f "$TMP_NAME"
                    else
                        log_ok "发现新版本，准备热更新"
                        NEED_UPDATE=1
                    fi
                else
                    log_warn "启动后更新检查失败，继续使用当前版本"
                    rm -f "$TMP_NAME"
                fi
            else
                check_and_update
            fi

            if [ "$NEED_UPDATE" -eq 1 ]; then
                log_step "执行热更新... (当前时间: $(date '+%Y-%m-%d %H:%M:%S'))"
                stop_program

                [ -f "$BINARY_NAME" ] && rm -f "$BINARY_NAME"
                mv "$TMP_NAME" "$BINARY_NAME"
                log_ok "热更新完成 (当前时间: $(date '+%Y-%m-%d %H:%M:%S'))"
                NEED_UPDATE=0

                # 热更新后打印下次预计检查时间（基于上次成功检查的时间戳）
                if [ -f "$PLUGIN_DIR/.last_update_check" ]; then
                    last_check=$(cat "$PLUGIN_DIR/.last_update_check" | cut -d'|' -f1)
                    next_ts=$((last_check + UPDATE_INTERVAL))
                    log_info "下次预计检查时间: $(format_timestamp "$next_ts") (间隔 $(format_duration $UPDATE_INTERVAL))"
                fi

                if ! start_program; then
                    log_error "热更新后启动失败，守护循环终止"
                    break
                fi
                CURRENT_DELAY=$RESTART_DELAY
            else
                # 进程存活但接口无响应（如死锁）时重启
                if ! check_health; then
                    stop_program
                    if ! start_program; then
                        log_error "健康检查重启失败，守护循环终止"
                        break
                    fi
                    continue
                fi
                sleep 10
            fi
        else
            # 程序已退出（异常或正常）
            if [ -n "$CHILD_PID" ]; then
                wait "$CHILD_PID" 2>/dev/null
                EXIT_CODE=$?

                log "========================================"
                log_info "程序已退出，退出码: $EXIT_CODE"
                if [ "$EXIT_CODE" -eq 0 ]; then
                    log_info "状态: 正常退出"
                else
                    log_error "状态: 异常退出"
                fi
                CHILD_PID=""
            fi

            # 退出后先尝试更新
            check_and_update

            if [ "$NEED_UPDATE" -eq 1 ]; then
                [ -f "$BINARY_NAME" ] && rm -f "$BINARY_NAME"
                mv "$TMP_NAME" "$BINARY_NAME"
                log_ok "已更新到新版本 (当前时间: $(date '+%Y-%m-%d %H:%M:%S'))"
                NEED_UPDATE=0
            fi

            # 指数退避重启
            log_info "等待 ${CURRENT_DELAY} 秒后重启..."
            sleep "$CURRENT_DELAY"

            CURRENT_DELAY=$((CURRENT_DELAY * 2))
            if [ "$CURRENT_DELAY" -gt "$MAX_RESTART_DELAY" ]; then
                CURRENT_DELAY=$MAX_RESTART_DELAY
            fi

            if ! start_program; then
                log_error "重启失败，守护循环终止"
                break
            fi
            CURRENT_DELAY=$RESTART_DELAY
        fi
    done
}

# ============ 启动 ============
main_loop
cleanup
