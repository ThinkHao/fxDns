#!/bin/bash

# Exit immediately if a command exits with a non-zero status.
set -e

# --- Configuration ---
APP_NAME="fxdns"
APP_USER_GROUP="fxdns" # User and group to run the service as

# Install paths
BIN_DIR="/usr/local/bin"
CONFIG_DIR="/etc/${APP_NAME}"
LOG_DIR="/var/log/${APP_NAME}"
# Source files (assumed to be in the same dir as setup.sh)
SOURCE_CONFIG_NAME="config.yaml.example"
BINARY_NAME="${APP_NAME}"

# Full target paths
BIN_PATH="${BIN_DIR}/${APP_NAME}"
TARGET_CONFIG_FILE_PATH="${CONFIG_DIR}/config.yaml"

# Service file paths
SYSTEMD_SERVICE_DIR="/etc/systemd/system"
SYSTEMD_SERVICE_FILE="${SYSTEMD_SERVICE_DIR}/${APP_NAME}.service"
INITD_SCRIPT_PATH="/etc/init.d/${APP_NAME}"

# --- Helper Functions ---
msg_info() {
    echo "[INFO] $1"
}

msg_error() {
    echo "[ERROR] $1" >&2
}

msg_warning() {
    echo "[WARN] $1" >&2
}

check_root() {
    if [ "$(id -u)" -ne 0 ]; then
        msg_error "此脚本必须以 root 用户权限运行。"
        exit 1
    fi
}

detect_init_system() {
    if [ -x "$(command -v systemctl)" ] && [ -d "/run/systemd/system" ]; then
        INIT_SYSTEM="systemd"
    elif [ -d "/etc/init.d" ]; then
        INIT_SYSTEM="init.d" # Basic fallback
    else
        msg_error "无法检测到 systemd 或 SysV init 系统。"
        exit 1
    fi
    msg_info "检测到初始化系统: ${INIT_SYSTEM}"
}

# --- Action Functions ---

create_user_and_group() {
    msg_info "创建用户和组 '${APP_USER_GROUP}'..."
    if ! getent group "${APP_USER_GROUP}" >/dev/null; then
        groupadd --system "${APP_USER_GROUP}"
        msg_info "用户组 '${APP_USER_GROUP}' 已创建。"
    else
        msg_info "用户组 '${APP_USER_GROUP}' 已存在。"
    fi

    if ! id "${APP_USER_GROUP}" >/dev/null 2>&1; then
        useradd --system --no-create-home --gid "${APP_USER_GROUP}" --shell /usr/sbin/nologin "${APP_USER_GROUP}"
        msg_info "用户 '${APP_USER_GROUP}' 已创建。"
    else
        msg_info "用户 '${APP_USER_GROUP}' 已存在。"
    fi
}

create_directories() {
    msg_info "创建必要的目录..."
    mkdir -p "${BIN_DIR}"
    mkdir -p "${CONFIG_DIR}"
    mkdir -p "${LOG_DIR}"
    chown -R "${APP_USER_GROUP}:${APP_USER_GROUP}" "${CONFIG_DIR}"
    chown -R "${APP_USER_GROUP}:${APP_USER_GROUP}" "${LOG_DIR}"
    msg_info "目录创建完成。"
}

copy_files() {
    local script_dir
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local source_binary_path="${script_dir}/${BINARY_NAME}"
    local source_config_path="${script_dir}/${SOURCE_CONFIG_NAME}"

    msg_info "复制文件..."
    if [ ! -f "${source_binary_path}" ]; then
        msg_error "二进制文件 '${source_binary_path}' 未找到。请确保它与 setup.sh 在同一目录。"
        exit 1
    fi
    cp "${source_binary_path}" "${BIN_PATH}"
    chmod 755 "${BIN_PATH}"
    chown root:root "${BIN_PATH}" # Binary ownership should be root for setcap
    msg_info "二进制文件已复制到 ${BIN_PATH}"

    if [ ! -f "${TARGET_CONFIG_FILE_PATH}" ]; then
        if [ ! -f "${source_config_path}" ]; then
            msg_warning "源配置文件 '${source_config_path}' 未找到。将创建一个空的配置文件于 ${TARGET_CONFIG_FILE_PATH}。"
            touch "${TARGET_CONFIG_FILE_PATH}" 
        else
            cp "${source_config_path}" "${TARGET_CONFIG_FILE_PATH}"
            msg_info "示例配置文件已从 ${source_config_path} 复制到 ${TARGET_CONFIG_FILE_PATH}"
        fi
        chown "${APP_USER_GROUP}:${APP_USER_GROUP}" "${TARGET_CONFIG_FILE_PATH}"
    else
        msg_info "配置文件 ${TARGET_CONFIG_FILE_PATH} 已存在，跳过复制示例配置。"
    fi
}

apply_setcap() {
    if ! command -v setcap >/dev/null; then
        msg_warning "'setcap' 命令未找到。无法授予绑定特权端口的能力。"
        msg_warning "请安装 'libcap2-bin' (Debian/Ubuntu) 或 'libcap' (RHEL/CentOS) 包。"
        msg_warning "程序可能无法在标准 DNS 端口，例如 53 端口，上监听。"
        return 1 # Indicate failure or inability to perform
    fi
    msg_info "授予 '${BIN_PATH}' CAP_NET_BIND_SERVICE 能力..."
    setcap 'cap_net_bind_service=+ep' "${BIN_PATH}"
    msg_info "能力已授予。"
}

install_systemd_service() {
    msg_info "安装 systemd 服务..."
    cat > "${SYSTEMD_SERVICE_FILE}" <<EOF
[Unit]
Description=${APP_NAME} DNS Proxy Server
After=network.target network-online.target
Wants=network-online.target

[Service]
Type=simple
User=${APP_USER_GROUP}
Group=${APP_USER_GROUP}
ExecStart=${BIN_PATH} -config ${TARGET_CONFIG_FILE_PATH}
WorkingDirectory=${CONFIG_DIR}
Restart=on-failure
RestartSec=5s
StandardOutput=append:${LOG_DIR}/${APP_NAME}.log
StandardError=append:${LOG_DIR}/${APP_NAME}.err.log

[Install]
WantedBy=multi-user.target
EOF
    chmod 644 "${SYSTEMD_SERVICE_FILE}"
    msg_info "重新加载 systemd daemon..."
    systemctl daemon-reload
    msg_info "启用 ${APP_NAME} 服务开机自启..."
    systemctl enable "${APP_NAME}.service"
    msg_info "${APP_NAME} systemd 服务已安装并启用。"
    msg_info "尝试启动 ${APP_NAME} 服务..."
    systemctl start "${APP_NAME}.service"
    msg_info "${APP_NAME} 服务已启动。请使用 'systemctl status ${APP_NAME}' 查看状态。"
}

install_initd_service() {
    msg_warning "SysV init 脚本安装暂未完全实现。"
    # Placeholder for init.d script creation
}

do_install() {
    msg_info "开始安装 ${APP_NAME}..."
    check_root
    detect_init_system

    create_user_and_group
    create_directories
    copy_files
    
    if ! apply_setcap; then
        msg_warning "Setcap 未成功应用。如果配置监听特权端口，例如 53 端口，服务可能无法启动。"
    fi

    if [ "${INIT_SYSTEM}" == "systemd" ]; then
        install_systemd_service
    elif [ "${INIT_SYSTEM}" == "init.d" ]; then
        install_initd_service
    else
        msg_error "不支持的初始化系统: ${INIT_SYSTEM}"
        exit 1
    fi
    msg_info "${APP_NAME} 安装完成。"
}

do_update() {
    msg_info "开始更新 ${APP_NAME}..."
    check_root
    detect_init_system

    local script_dir
    script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
    local source_binary_path="${script_dir}/${BINARY_NAME}"

    if [ ! -f "${source_binary_path}" ]; then
        msg_error "新的二进制文件 '${source_binary_path}' 未找到。请确保它与 setup.sh 在同一目录。"
        exit 1
    fi
    
    msg_info "停止 ${APP_NAME} 服务..."
    if [ "${INIT_SYSTEM}" == "systemd" ]; then
        systemctl stop "${APP_NAME}.service" || msg_warning "停止服务失败，可能服务未运行。"
    elif [ "${INIT_SYSTEM}" == "init.d" ]; then
        # service "${APP_NAME}" stop || msg_warning "停止服务失败，可能服务未运行。"
        msg_warning "init.d 服务停止暂未实现"
    fi

    msg_info "复制新的二进制文件..."
    cp "${source_binary_path}" "${BIN_PATH}"
    chmod 755 "${BIN_PATH}"
    chown root:root "${BIN_PATH}" # Ensure root ownership for setcap

    if ! apply_setcap; then
         msg_warning "Setcap 未成功应用。如果配置监听特权端口 (如 53)，服务可能无法启动。"
    fi
    
    msg_info "重新启动 ${APP_NAME} 服务..."
    if [ "${INIT_SYSTEM}" == "systemd" ]; then
        systemctl start "${APP_NAME}.service"
    elif [ "${INIT_SYSTEM}" == "init.d" ]; then
        # service "${APP_NAME}" start
        msg_warning "init.d 服务启动暂未实现"
    fi
    msg_info "${APP_NAME} 更新完成。"
}

do_uninstall() {
    msg_info "开始卸载 ${APP_NAME}..."
    check_root
    detect_init_system

    msg_info "停止 ${APP_NAME} 服务..."
    if [ "${INIT_SYSTEM}" == "systemd" ]; then
        systemctl stop "${APP_NAME}.service" || msg_warning "停止服务失败，可能服务未运行。"
        systemctl disable "${APP_NAME}.service" || msg_warning "禁用服务失败。"
        rm -f "${SYSTEMD_SERVICE_FILE}"
        systemctl daemon-reload
        systemctl reset-failed # Optional
        msg_info "Systemd 服务已移除。"
    elif [ "${INIT_SYSTEM}" == "init.d" ]; then
        # service "${APP_NAME}" stop || msg_warning "停止服务失败，可能服务未运行。"
        # update-rc.d -f ${APP_NAME} remove # Debian/Ubuntu
        # chkconfig --del ${APP_NAME} # RHEL/CentOS
        # rm -f "${INITD_SCRIPT_PATH}"
        msg_warning "SysV init 服务移除暂未完全实现。"
    fi

    msg_info "移除文件..."
    rm -f "${BIN_PATH}"
    
    read -p "是否移除配置文件目录 ${CONFIG_DIR} (及其内容)? [y/N]: " -r confirm_remove_config
    if [[ "${confirm_remove_config}" =~ ^[yY](es)?$ ]]; then
        rm -rf "${CONFIG_DIR}"
        msg_info "配置文件目录已移除。"
    fi

    read -p "是否移除日志文件目录 ${LOG_DIR} (及其内容)? [y/N]: " -r confirm_remove_logs
    if [[ "${confirm_remove_logs}" =~ ^[yY](es)?$ ]]; then
        rm -rf "${LOG_DIR}"
        msg_info "日志文件目录已移除。"
    fi
    
    msg_info "${APP_NAME} 卸载完成。"
}


usage() {
    echo "用法: sudo $0 {install|update|uninstall}"
    echo "  install    : 安装 ${APP_NAME} 服务。"
    echo "  update     : 更新 ${APP_NAME} 二进制文件。"
    echo "  uninstall  : 卸载 ${APP_NAME} 服务。"
    exit 1
}

# --- Main Logic ---
if [ $# -eq 0 ]; then
    usage
fi

ACTION="$1"
shift # Remove the first argument (action)

case "${ACTION}" in
    install)
        do_install "$@"
        ;;
    update)
        do_update "$@"
        ;;
    uninstall)
        do_uninstall "$@"
        ;;
    *)
        usage
        ;;
esac

exit 0
