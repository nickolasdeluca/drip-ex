#!/bin/bash
set -e

# ============================================================================
# Configuration
# ============================================================================
GITHUB_REPO="${GITHUB_REPO:-nickolasdeluca/drip-ex}"
INSTALL_DIR="${INSTALL_DIR:-}"
VERSION="${VERSION:-}"
BINARY_NAME="drip"
UNINSTALL_MODE=false
COMMAND_MADE_AVAILABLE=false
IS_UPDATE=false

# Scratch directory for the download, removed on exit.
TMP_WORK_DIR=""
DOWNLOADED_BINARY=""

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m'

# Language (default: en)
LANG_CODE="${LANG_CODE:-en}"

# ============================================================================
# Internationalization
# ============================================================================
msg_en() {
    case "$1" in
        banner_title) echo "Drip Client - One-Click Installer" ;;
        select_lang) echo "Select language / 选择语言" ;;
        lang_en) echo "English" ;;
        lang_zh) echo "中文" ;;
        checking_os) echo "Checking operating system..." ;;
        detected_os) echo "Detected OS" ;;
        unsupported_os) echo "Unsupported operating system" ;;
        checking_arch) echo "Checking system architecture..." ;;
        detected_arch) echo "Detected architecture" ;;
        unsupported_arch) echo "Unsupported architecture" ;;
        checking_deps) echo "Checking dependencies..." ;;
        deps_ok) echo "Dependencies check passed" ;;
        downloading) echo "Downloading Drip client" ;;
        download_failed) echo "Download failed" ;;
        download_ok) echo "Download completed" ;;
        select_install_dir) echo "Select installation directory" ;;
        option_user) echo "User directory (no sudo required)" ;;
        option_system) echo "System directory (requires sudo)" ;;
        option_current) echo "Current directory" ;;
        option_custom) echo "Custom path" ;;
        enter_custom_path) echo "Enter custom path" ;;
        installing) echo "Installing binary..." ;;
        install_ok) echo "Installation completed" ;;
        updating_path) echo "Updating PATH..." ;;
        path_updated) echo "PATH updated" ;;
        path_note) echo "Please restart your terminal or run: source ~/.bashrc" ;;
        config_title) echo "Client Configuration" ;;
        configure_now) echo "Configure client now?" ;;
        enter_server) echo "Enter server address (e.g., tunnel.example.com:443, port defaults to 443)" ;;
        server_required) echo "Server address is required" ;;
        enter_token) echo "Enter authentication token" ;;
        token_required) echo "Token is required" ;;
        skip_verify) echo "Skip TLS certificate verification? (for self-signed certs)" ;;
        config_saved) echo "Configuration saved" ;;
        config_failed) echo "Failed to save configuration" ;;
        add_tunnel_q) echo "Add a tunnel to the configuration now?" ;;
        enter_local_port) echo "Local port to expose (e.g., 3000)" ;;
        port_required) echo "A port between 1 and 65535 is required" ;;
        enter_tunnel_type) echo "Tunnel type: http, https or tcp [http]" ;;
        enter_tunnel_name) echo "Name for this tunnel" ;;
        enter_subdomain) echo "Subdomain (optional; empty takes the allocation reserved for this machine)" ;;
        tunnel_added) echo "Tunnel added" ;;
        tunnel_failed) echo "Failed to add the tunnel" ;;
        install_complete) echo "Installation completed!" ;;
        usage_title) echo "Usage" ;;
        usage_http) echo "Expose HTTP service on port 3000" ;;
        usage_tcp) echo "Expose TCP service on port 5432" ;;
        usage_config) echo "Show/modify configuration" ;;
        usage_daemon) echo "Run as background daemon" ;;
        run_test) echo "Test connection now?" ;;
        test_running) echo "Testing connection..." ;;
        test_success) echo "Connection successful" ;;
        test_failed) echo "Connection failed" ;;
        yes) echo "y" ;;
        no) echo "n" ;;
        press_enter) echo "Press Enter to continue..." ;;
        windows_note) echo "For Windows, please download the .exe file from GitHub Releases" ;;
        verifying_checksum) echo "Verifying the download..." ;;
        checksum_ok) echo "Checksum verified" ;;
        checksum_missing) echo "The release has no checksum file; refusing to install an unverified binary" ;;
        checksum_unknown) echo "The checksum file does not list this archive" ;;
        checksum_failed) echo "Checksum mismatch; the download is corrupt or tampered with" ;;
        no_sha256) echo "No SHA-256 tool found (sha256sum, shasum or openssl)" ;;
        already_installed) echo "Drip is already installed" ;;
        current_version) echo "Current version" ;;
        update_now) echo "Update to the latest version?" ;;
        updating) echo "Updating..." ;;
        update_ok) echo "Update completed" ;;
        verify_install) echo "Verifying installation..." ;;
        verify_ok) echo "Verification passed" ;;
        verify_failed) echo "Verification failed" ;;
        insecure_note) echo "Not stored in the config: pass -k on each drip command (development/testing only)" ;;
        uninstall_title) echo "Drip Client - Uninstall" ;;
        uninstall_prompt) echo "Uninstall Drip client now?" ;;
        uninstalling) echo "Uninstalling Drip client..." ;;
        uninstall_done) echo "Uninstall completed" ;;
        uninstall_not_found) echo "Drip is not installed" ;;
        remove_config_prompt) echo "Remove Drip config directory as well?" ;;
        config_removed) echo "Config removed" ;;
        path_cleanup) echo "Cleaning PATH entries..." ;;
        path_cleanup_done) echo "PATH entries cleaned" ;;
        *) echo "$1" ;;
    esac
}

msg_zh() {
    case "$1" in
        banner_title) echo "Drip 客户端 - 一键安装脚本" ;;
        select_lang) echo "Select language / 选择语言" ;;
        lang_en) echo "English" ;;
        lang_zh) echo "中文" ;;
        checking_os) echo "检查操作系统..." ;;
        detected_os) echo "检测到操作系统" ;;
        unsupported_os) echo "不支持的操作系统" ;;
        checking_arch) echo "检查系统架构..." ;;
        detected_arch) echo "检测到架构" ;;
        unsupported_arch) echo "不支持的架构" ;;
        checking_deps) echo "检查依赖..." ;;
        deps_ok) echo "依赖检查通过" ;;
        downloading) echo "下载 Drip 客户端" ;;
        download_failed) echo "下载失败" ;;
        download_ok) echo "下载完成" ;;
        select_install_dir) echo "选择安装目录" ;;
        option_user) echo "用户目录（无需 sudo）" ;;
        option_system) echo "系统目录（需要 sudo）" ;;
        option_current) echo "当前目录" ;;
        option_custom) echo "自定义路径" ;;
        enter_custom_path) echo "输入自定义路径" ;;
        installing) echo "安装二进制文件..." ;;
        install_ok) echo "安装完成" ;;
        updating_path) echo "更新 PATH..." ;;
        path_updated) echo "PATH 已更新" ;;
        path_note) echo "请重启终端或运行: source ~/.bashrc" ;;
        config_title) echo "客户端配置" ;;
        configure_now) echo "现在配置客户端？" ;;
        enter_server) echo "输入服务器地址（例如：tunnel.example.com:443，端口默认 443）" ;;
        server_required) echo "服务器地址是必填项" ;;
        enter_token) echo "输入认证令牌" ;;
        token_required) echo "认证令牌是必填项" ;;
        skip_verify) echo "跳过 TLS 证书验证？（用于自签名证书）" ;;
        config_saved) echo "配置已保存" ;;
        config_failed) echo "配置保存失败" ;;
        add_tunnel_q) echo "现在向配置中添加隧道？" ;;
        enter_local_port) echo "要暴露的本地端口（例如：3000）" ;;
        port_required) echo "端口必须在 1 到 65535 之间" ;;
        enter_tunnel_type) echo "隧道类型：http、https 或 tcp [http]" ;;
        enter_tunnel_name) echo "该隧道的名称" ;;
        enter_subdomain) echo "子域名（可选；留空则使用为该机器预留的分配）" ;;
        tunnel_added) echo "隧道已添加" ;;
        tunnel_failed) echo "添加隧道失败" ;;
        install_complete) echo "安装完成！" ;;
        usage_title) echo "使用方法" ;;
        usage_http) echo "暴露本地 3000 端口的 HTTP 服务" ;;
        usage_tcp) echo "暴露本地 5432 端口的 TCP 服务" ;;
        usage_config) echo "显示/修改配置" ;;
        usage_daemon) echo "作为后台守护进程运行" ;;
        run_test) echo "现在测试连接？" ;;
        test_running) echo "正在测试连接..." ;;
        test_success) echo "连接成功" ;;
        test_failed) echo "连接失败" ;;
        yes) echo "y" ;;
        no) echo "n" ;;
        press_enter) echo "按 Enter 继续..." ;;
        windows_note) echo "Windows 用户请从 GitHub Releases 下载 .exe 文件" ;;
        verifying_checksum) echo "正在校验下载文件..." ;;
        checksum_ok) echo "校验通过" ;;
        checksum_missing) echo "该版本没有校验文件；拒绝安装未经校验的二进制文件" ;;
        checksum_unknown) echo "校验文件中没有该压缩包" ;;
        checksum_failed) echo "校验值不匹配；下载文件已损坏或被篡改" ;;
        no_sha256) echo "未找到 SHA-256 工具（sha256sum、shasum 或 openssl）" ;;
        already_installed) echo "Drip 已安装" ;;
        current_version) echo "当前版本" ;;
        update_now) echo "是否更新到最新版本？" ;;
        updating) echo "正在更新..." ;;
        update_ok) echo "更新完成" ;;
        verify_install) echo "验证安装..." ;;
        verify_ok) echo "验证通过" ;;
        verify_failed) echo "验证失败" ;;
        insecure_note) echo "该选项不会写入配置：请在每条 drip 命令后加 -k（仅用于开发/测试）" ;;
        uninstall_title) echo "Drip 客户端 - 卸载" ;;
        uninstall_prompt) echo "现在卸载 Drip 客户端？" ;;
        uninstalling) echo "正在卸载 Drip 客户端..." ;;
        uninstall_done) echo "卸载完成" ;;
        uninstall_not_found) echo "未检测到已安装的 Drip" ;;
        remove_config_prompt) echo "是否同时删除 Drip 配置目录？" ;;
        config_removed) echo "配置已删除" ;;
        path_cleanup) echo "清理 PATH 相关配置..." ;;
        path_cleanup_done) echo "PATH 配置已清理" ;;
        *) echo "$1" ;;
    esac
}

# Get message by key (bash 3.2 compatible)
msg() {
    local key="$1"
    if [[ "$LANG_CODE" == "zh" ]]; then
        msg_zh "$key"
    else
        msg_en "$key"
    fi
}

# Prompt helper compatible with bash 3.2 and zsh
prompt_input() {
    local __prompt="$1"
    local __var_name="$2"
    printf "%s" "$__prompt"
    IFS= read -r "$__var_name" < /dev/tty
}

# ============================================================================
# Output functions
# ============================================================================
print_info() { echo -e "${BLUE}[INFO]${NC} $1"; }
print_success() { echo -e "${GREEN}[✓]${NC} $1"; }
print_warning() { echo -e "${YELLOW}[!]${NC} $1"; }
print_error() { echo -e "${RED}[✗]${NC} $1"; }
print_step() { echo -e "${CYAN}[→]${NC} $1"; }

repeat_char() {
    local char="$1"
    local count="$2"
    local out=""
    for _ in $(seq 1 "$count"); do
        out+="$char"
    done
    echo "$out"
}

print_panel() {
    local title="$1"
    shift
    local width=58
    local bar
    bar=$(repeat_char "=" "$width")
    echo ""
    echo -e "${CYAN}${bar}${NC}"
    echo -e "${CYAN}${title}${NC}"
    echo -e "${CYAN}${bar}${NC}"
    for line in "$@"; do
        echo -e "  $line"
    done
    echo -e "${CYAN}${bar}${NC}"
    echo ""
}

print_subheader() {
    local title="$1"
    local width=58
    local bar
    bar=$(repeat_char "-" "$width")
    echo ""
    echo -e "${CYAN}${title}${NC}"
    echo -e "${CYAN}${bar}${NC}"
}

# Print banner
print_banner() {
    echo -e "${GREEN}"
    cat << "EOF"
    ____       _
   / __ \_____(_)___
  / / / / ___/ / __ \
 / /_/ / /  / / /_/ /
/_____/_/  /_/ .___/
            /_/
EOF
    echo -e "${BOLD}$(msg banner_title)${NC}"
    echo ""
}

# Extract version from a drip binary, preferring the plain output when available
get_version_from_binary() {
    local binary="$1"
    local output=""
    local version=""

    output=$("$binary" version --short 2>/dev/null || true)
    if [[ -n "$output" ]]; then
        version=$(printf '%s\n' "$output" | awk -F': ' '/Version/ {print $2; exit}')
    fi

    if [[ -z "$version" ]]; then
        output=$("$binary" version 2>/dev/null || true)
        if [[ -n "$output" ]]; then
            output=$(printf '%s\n' "$output" | sed -E $'s/\x1b\\[[0-9;]*[A-Za-z]//g')
            version=$(printf '%s\n' "$output" | sed -nE 's/.*Version:[[:space:]]*([vV]?[0-9][^[:space:]]*).*/\1/p' | head -n1)
        fi
    fi

    echo "${version:-unknown}"
}

# ============================================================================
# Language selection
# ============================================================================
select_language() {
    print_panel "$(msg select_lang)" \
        "${GREEN}1)${NC} English" \
        "${GREEN}2)${NC} 中文"

    prompt_input "Select [1]: " lang_choice
    case "$lang_choice" in
        2)
            LANG_CODE="zh"
            ;;
        *)
            LANG_CODE="en"
            ;;
    esac
    echo ""
}

# ============================================================================
# System checks
# ============================================================================
check_os() {
    print_step "$(msg checking_os)"

    case "$(uname -s)" in
        Linux*)
            OS="linux"
            ;;
        Darwin*)
            OS="darwin"
            ;;
        MINGW*|MSYS*|CYGWIN*)
            OS="windows"
            print_warning "$(msg windows_note)"
            ;;
        *)
            print_error "$(msg unsupported_os): $(uname -s)"
            exit 1
            ;;
    esac

    print_success "$(msg detected_os): $OS"
}

check_arch() {
    print_step "$(msg checking_arch)"

    case "$(uname -m)" in
        x86_64|amd64)
            ARCH="amd64"
            ;;
        aarch64|arm64)
            ARCH="arm64"
            ;;
        armv7l)
            ARCH="armv7"
            ;;
        armv6l)
            ARCH="armv6"
            ;;
        i386|i686)
            ARCH="386"
            ;;
        *)
            print_error "$(msg unsupported_arch): $(uname -m)"
            exit 1
            ;;
    esac

    print_success "$(msg detected_arch): $ARCH"
}

check_dependencies() {
    print_step "$(msg checking_deps)"

    # Check for download tool
    if ! command -v curl &> /dev/null && ! command -v wget &> /dev/null; then
        print_error "curl or wget is required"
        exit 1
    fi

    print_success "$(msg deps_ok)"
}

get_latest_version() {
    # Get latest version from GitHub API
    local api_url="https://api.github.com/repos/${GITHUB_REPO}/releases/latest"
    local version=""

    if command -v curl &> /dev/null; then
        version=$(curl -fsSL "$api_url" | grep '"tag_name":' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' 2>/dev/null)
    else
        version=$(wget -qO- "$api_url" | grep '"tag_name":' | sed -E 's/.*"tag_name": *"([^"]+)".*/\1/' 2>/dev/null)
    fi

    if [[ -z "$version" ]]; then
        print_error "Failed to get latest version from GitHub"
        exit 1
    fi

    echo "$version"
}


check_existing_install() {
    if command -v drip &> /dev/null; then
        local current_path=$(command -v drip)
        local current_version=$(get_version_from_binary "drip")

        print_warning "$(msg already_installed): $current_path"
        print_info "$(msg current_version): $current_version"

        # Check remote version
        print_step "Checking for updates..."
        local latest_version=$(get_latest_version)

        if [[ "$current_version" == "$latest_version" ]]; then
            print_success "Already up to date ($current_version)"
            exit 0
        else
            print_info "Latest version: $latest_version"
            echo ""
            prompt_input "$(msg update_now) [Y/n]: " update_choice
        fi

        if [[ "$update_choice" =~ ^[Nn]$ ]]; then
            exit 0
        fi

        INSTALL_DIR=$(dirname "$current_path")
        IS_UPDATE=true
    fi
}

# ============================================================================
# Download and install
# ============================================================================
# resolve_version fills VERSION in the caller's shell. It must not be run from a
# command substitution: the assignment would be lost with the subshell, leaving
# the download URL without a version.
resolve_version() {
    if [[ -z "$VERSION" ]]; then
        VERSION=$(get_latest_version)
    fi
}

get_archive_name() {
    # Strip 'v' prefix for archive filename (v0.7.0 -> 0.7.0)
    local version_number="${VERSION#v}"
    local ext="tar.gz"

    if [[ "$OS" == "windows" ]]; then
        echo "drip_${version_number}_windows_${ARCH}.${ext}"
    else
        echo "drip_${version_number}_${OS}_${ARCH}.${ext}"
    fi
}

get_checksums_name() {
    local version_number="${VERSION#v}"
    echo "drip_${version_number}_checksums.txt"
}

get_download_url() {
    echo "https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/$(get_archive_name)"
}

download_binary() {
    resolve_version

    local archive_name
    archive_name=$(get_archive_name)
    local url
    url=$(get_download_url)
    local checksums_url="https://github.com/${GITHUB_REPO}/releases/download/${VERSION}/$(get_checksums_name)"

    if [[ "$IS_UPDATE" == true ]]; then
        print_step "$(msg updating)..."
    else
        print_step "$(msg downloading)..."
    fi

    # A private scratch directory; fixed /tmp paths are writable by every local
    # user, and this script may run under sudo.
    TMP_WORK_DIR=$(mktemp -d)
    local tmp_archive="${TMP_WORK_DIR}/${archive_name}"
    local tmp_checksums="${TMP_WORK_DIR}/checksums.txt"
    local tmp_dir="${TMP_WORK_DIR}/extract"
    mkdir -p "$tmp_dir"

    if command -v curl &> /dev/null; then
        # Use -# for progress bar instead of -s (silent)
        if ! curl -f#L "$url" -o "$tmp_archive"; then
            print_error "$(msg download_failed): $url"
            exit 1
        fi
    else
        # Use --show-progress to display download progress
        if ! wget --show-progress "$url" -O "$tmp_archive" 2>&1 | grep -v "^$"; then
            print_error "$(msg download_failed): $url"
            exit 1
        fi
    fi

    verify_checksum "$tmp_archive" "$archive_name" "$checksums_url" "$tmp_checksums"

    # Extract the archive
    if ! tar -xzf "$tmp_archive" -C "$tmp_dir"; then
        print_error "Failed to extract archive"
        exit 1
    fi

    # Find the binary (it should be named 'drip' or 'drip.exe')
    local extracted_binary=""
    if [[ "$OS" == "windows" ]]; then
        extracted_binary=$(find "$tmp_dir" -name "drip.exe" -type f 2>/dev/null | head -1)
    else
        extracted_binary=$(find "$tmp_dir" -name "drip" -type f 2>/dev/null | head -1)
    fi

    if [[ -z "$extracted_binary" ]]; then
        print_error "Binary not found in archive"
        exit 1
    fi

    DOWNLOADED_BINARY="${TMP_WORK_DIR}/drip-download"
    mv "$extracted_binary" "$DOWNLOADED_BINARY"
    chmod +x "$DOWNLOADED_BINARY"

    print_success "$(msg download_ok)"
}

# sha256_of prints the SHA-256 of a file, trying the tools most likely to be
# present. macOS ships shasum rather than sha256sum.
sha256_of() {
    local file="$1"

    if command -v sha256sum &> /dev/null; then
        sha256sum "$file" | awk '{print $1}'
    elif command -v shasum &> /dev/null; then
        shasum -a 256 "$file" | awk '{print $1}'
    elif command -v openssl &> /dev/null; then
        openssl dgst -sha256 "$file" | awk '{print $NF}'
    else
        return 1
    fi
}

# verify_checksum refuses to continue unless the archive matches the SHA-256 the
# release publishes for it. Failing closed is the point: this script is run by
# piping a URL into a shell, so the binary it fetches deserves a check.
verify_checksum() {
    local archive="$1"
    local archive_name="$2"
    local checksums_url="$3"
    local checksums_file="$4"

    print_step "$(msg verifying_checksum)"

    local actual
    if ! actual=$(sha256_of "$archive"); then
        print_error "$(msg no_sha256)"
        exit 1
    fi

    if command -v curl &> /dev/null; then
        if ! curl -fsSL "$checksums_url" -o "$checksums_file"; then
            print_error "$(msg checksum_missing)"
            exit 1
        fi
    else
        if ! wget -q "$checksums_url" -O "$checksums_file"; then
            print_error "$(msg checksum_missing)"
            exit 1
        fi
    fi

    # goreleaser writes "<sha256>  <filename>"; some tools prefix the name with
    # an asterisk to mark binary mode.
    local expected
    expected=$(awk -v name="$archive_name" \
        '$2 == name || $2 == "*" name { print $1; exit }' "$checksums_file")

    if [[ -z "$expected" ]]; then
        print_error "$(msg checksum_unknown): $archive_name"
        exit 1
    fi

    if [[ "$expected" != "$actual" ]]; then
        print_error "$(msg checksum_failed)"
        print_info "expected: $expected"
        print_info "actual:   $actual"
        exit 1
    fi

    print_success "$(msg checksum_ok)"
}

select_install_dir() {
    if [[ -n "$INSTALL_DIR" ]]; then
        return
    fi

    print_panel "$(msg select_install_dir)" \
        "${GREEN}1)${NC} ~/.local/bin $(msg option_user)" \
        "${GREEN}2)${NC} /usr/local/bin $(msg option_system)" \
        "${GREEN}3)${NC} ./ $(msg option_current)" \
        "${GREEN}4)${NC} $(msg option_custom)"

    prompt_input "Select [1]: " dir_choice

    case "$dir_choice" in
        2)
            INSTALL_DIR="/usr/local/bin"
            NEED_SUDO=true
            ;;
        3)
            INSTALL_DIR="."
            ;;
        4)
            prompt_input "$(msg enter_custom_path): " INSTALL_DIR
            ;;
        *)
            INSTALL_DIR="$HOME/.local/bin"
            ;;
    esac
}

install_binary() {
    print_step "$(msg installing)"

    # Create directory if needed
    if [[ ! -d "$INSTALL_DIR" ]]; then
        if [[ "$NEED_SUDO" == true ]]; then
            sudo mkdir -p "$INSTALL_DIR"
        else
            mkdir -p "$INSTALL_DIR"
        fi
    fi

    local target_path="$INSTALL_DIR/$BINARY_NAME"
    if [[ "$OS" == "windows" ]]; then
        target_path="$INSTALL_DIR/$BINARY_NAME.exe"
    fi

    # Install binary
    if [[ "$NEED_SUDO" == true ]]; then
        sudo mv "$DOWNLOADED_BINARY" "$target_path"
        sudo chmod +x "$target_path"
    else
        mv "$DOWNLOADED_BINARY" "$target_path"
        chmod +x "$target_path"
    fi

    print_success "$(msg install_ok): $target_path"
}

update_path() {
    # Skip if already in PATH
    if command -v drip &> /dev/null; then
        return
    fi
    if [[ "$COMMAND_MADE_AVAILABLE" == true ]]; then
        return
    fi

    # Skip for system directories (usually already in PATH)
    if [[ "$INSTALL_DIR" == "/usr/local/bin" ]] || [[ "$INSTALL_DIR" == "/usr/bin" ]]; then
        return
    fi

    print_step "$(msg updating_path)"

    local shell_rc=""
    local export_line="export PATH=\"\$PATH:$INSTALL_DIR\""

    # Determine shell config file
    if [[ -n "$ZSH_VERSION" ]] || [[ "$SHELL" == *"zsh"* ]]; then
        shell_rc="$HOME/.zshrc"
    elif [[ -n "$BASH_VERSION" ]] || [[ "$SHELL" == *"bash"* ]]; then
        if [[ "$OS" == "darwin" ]]; then
            shell_rc="$HOME/.bash_profile"
        else
            shell_rc="$HOME/.bashrc"
        fi
    elif [[ "$SHELL" == *"fish"* ]]; then
        shell_rc="$HOME/.config/fish/config.fish"
        export_line="set -gx PATH \$PATH $INSTALL_DIR"
    fi

    if [[ -n "$shell_rc" ]]; then
        # Check if already added
        if ! grep -q "$INSTALL_DIR" "$shell_rc" 2>/dev/null; then
            echo "" >> "$shell_rc"
            echo "# Drip client" >> "$shell_rc"
            echo "$export_line" >> "$shell_rc"
            print_success "$(msg path_updated): $shell_rc"
        fi
    fi

    print_warning "$(msg path_note)"
}

ensure_command_access() {
    # Try to make the command available immediately without requiring shell restart
    if [[ "$OS" == "windows" ]]; then
        return
    fi

    hash -r 2>/dev/null || true
    command -v rehash >/dev/null 2>&1 && rehash || true

    if command -v "$BINARY_NAME" >/dev/null 2>&1; then
        COMMAND_MADE_AVAILABLE=true
        return
    fi

    local target_path="$INSTALL_DIR/$BINARY_NAME"
    local preferred="/usr/local/bin"

    if [[ ":$PATH:" == *":$preferred:"* ]]; then
        if [[ -w "$preferred" ]]; then
            if ln -sf "$target_path" "$preferred/$BINARY_NAME" 2>/dev/null; then
                COMMAND_MADE_AVAILABLE=true
                print_success "Made ${BINARY_NAME} available at $preferred/$BINARY_NAME"
            fi
        else
            if sudo ln -sf "$target_path" "$preferred/$BINARY_NAME" 2>/dev/null; then
                COMMAND_MADE_AVAILABLE=true
                print_success "Made ${BINARY_NAME} available at $preferred/$BINARY_NAME"
            fi
        fi
    fi

    # Refresh hash table
    hash -r 2>/dev/null || true
    command -v rehash >/dev/null 2>&1 && rehash || true
}

verify_installation() {
    print_step "$(msg verify_install)"

    local binary_path="$INSTALL_DIR/$BINARY_NAME"
    if [[ "$OS" == "windows" ]]; then
        binary_path="$INSTALL_DIR/$BINARY_NAME.exe"
    fi

    if [[ -x "$binary_path" ]]; then
        local version=$(get_version_from_binary "$binary_path")
        print_success "$(msg verify_ok): $version"
    else
        print_error "$(msg verify_failed)"
        exit 1
    fi
}

# ============================================================================
# Configuration
# ============================================================================
configure_client() {
    echo ""
    prompt_input "$(msg configure_now) [Y/n]: " config_choice

    if [[ "$config_choice" =~ ^[Nn]$ ]]; then
        return
    fi

    print_subheader "$(msg config_title)"

    local binary_path="$INSTALL_DIR/$BINARY_NAME"

    # Server address
    while true; do
        prompt_input "$(msg enter_server): " SERVER
        if [[ -n "$SERVER" ]]; then
            break
        fi
        print_error "$(msg server_required)"
    done

    # Token
    while true; do
        prompt_input "$(msg enter_token): " TOKEN
        if [[ -n "$TOKEN" ]]; then
            break
        fi
        print_error "$(msg token_required)"
    done

    # Insecure mode
    prompt_input "$(msg skip_verify) [y/N]: " insecure_choice
    if [[ "$insecure_choice" =~ ^[Yy]$ ]]; then
        print_warning "$(msg insecure_note)"
    fi

    # Save configuration
    if "$binary_path" config set --server "$SERVER" --token "$TOKEN"; then
        print_success "$(msg config_saved)"
    else
        print_error "$(msg config_failed)"
        return
    fi

    configure_tunnel "$binary_path"
}

# configure_tunnel records what this machine exposes. The allocation on the
# server decides the public name; this decides the local port behind it, and
# 'drip start' has nothing to run without it.
configure_tunnel() {
    local binary_path="$1"

    prompt_input "$(msg add_tunnel_q) [Y/n]: " tunnel_choice
    if [[ "$tunnel_choice" =~ ^[Nn]$ ]]; then
        return
    fi

    local port=""
    while true; do
        prompt_input "$(msg enter_local_port): " port
        if [[ "$port" =~ ^[0-9]+$ ]] && (( port >= 1 && port <= 65535 )); then
            break
        fi
        print_error "$(msg port_required)"
    done

    local type=""
    prompt_input "$(msg enter_tunnel_type): " type
    type="${type:-http}"

    local name=""
    prompt_input "$(msg enter_tunnel_name) [$type-$port]: " name
    name="${name:-$type-$port}"

    local subdomain=""
    prompt_input "$(msg enter_subdomain): " subdomain

    local args=(config tunnel add --name "$name" --type "$type" --port "$port" --replace)
    if [[ -n "$subdomain" ]]; then
        args+=(--subdomain "$subdomain")
    fi

    if "$binary_path" "${args[@]}"; then
        print_success "$(msg tunnel_added)"
    else
        print_error "$(msg tunnel_failed)"
    fi
}

# ============================================================================
# Test connection
# ============================================================================
test_connection() {
    echo ""
    prompt_input "$(msg run_test) [y/N]: " test_choice

    if [[ ! "$test_choice" =~ ^[Yy]$ ]]; then
        return
    fi

    print_step "$(msg test_running)"

    local binary_path="$INSTALL_DIR/$BINARY_NAME"

    # Try to validate config
    if "$binary_path" config validate 2>/dev/null; then
        print_success "$(msg test_success)"
    else
        print_warning "$(msg test_failed)"
    fi
}

# ============================================================================
# Final output
# ============================================================================
show_completion() {
    local binary_path="$INSTALL_DIR/$BINARY_NAME"

    print_panel "$(msg install_complete)"

    echo -e "${CYAN}$(msg usage_title):${NC}"
    echo ""
    echo -e "  ${GREEN}# $(msg usage_http)${NC}"
    echo -e "  ${YELLOW}$BINARY_NAME http 3000${NC}"
    echo ""
    echo -e "  ${GREEN}# $(msg usage_tcp)${NC}"
    echo -e "  ${YELLOW}$BINARY_NAME tcp 5432${NC}"
    echo ""
    echo -e "  ${GREEN}# $(msg usage_config)${NC}"
    echo -e "  ${YELLOW}$BINARY_NAME config show${NC}"
    echo -e "  ${YELLOW}$BINARY_NAME config init${NC}"
    echo ""
    echo -e "  ${GREEN}# $(msg usage_daemon)${NC}"
    echo -e "  ${YELLOW}$BINARY_NAME daemon start http 3000${NC}"
    echo -e "  ${YELLOW}$BINARY_NAME daemon list${NC}"
    echo ""
}

# ============================================================================
# Uninstall
# ============================================================================
cleanup_path_entries() {
    local install_dir="$1"

    local candidates=()

    local os_name="$OS"
    if [[ -z "$os_name" ]]; then
        case "$(uname -s)" in
            Darwin*) os_name="darwin" ;;
            *) os_name="linux" ;;
        esac
    fi

    # Determine shell config files
    if [[ -n "$ZSH_VERSION" ]] || [[ "$SHELL" == *"zsh"* ]]; then
        candidates+=("$HOME/.zshrc")
    fi
    if [[ -n "$BASH_VERSION" ]] || [[ "$SHELL" == *"bash"* ]]; then
        if [[ "$os_name" == "darwin" ]]; then
            candidates+=("$HOME/.bash_profile")
        else
            candidates+=("$HOME/.bashrc")
        fi
    fi
    if [[ "$SHELL" == *"fish"* ]]; then
        candidates+=("$HOME/.config/fish/config.fish")
    fi

    for file in "${candidates[@]}"; do
        if [[ -f "$file" ]] && grep -q "$install_dir" "$file" 2>/dev/null; then
            local tmp
            tmp=$(mktemp)
            # Remove the comment marker and PATH export line we added
            grep -v "Drip client" "$file" | grep -v "$install_dir" > "$tmp" || true
            mv "$tmp" "$file"
        fi
    done
}

remove_config_dirs() {
    local removed=false
    local dirs=("$HOME/.drip" "$HOME/.config/drip")

    echo ""
    prompt_input "$(msg remove_config_prompt) [y/N]: " remove_choice
    if [[ ! "$remove_choice" =~ ^[Yy]$ ]]; then
        return
    fi

    for dir in "${dirs[@]}"; do
        if [[ -d "$dir" ]]; then
            rm -rf "$dir"
            removed=true
        fi
    done

    if [[ "$removed" == true ]]; then
        print_success "$(msg config_removed)"
    fi
}

uninstall_client() {
    if ! command -v drip &> /dev/null; then
        print_warning "$(msg uninstall_not_found)"
        return
    fi

    local current_path
    current_path=$(command -v drip)

    prompt_input "$(msg uninstall_prompt) [y/N]: " confirm_uninstall
    if [[ ! "$confirm_uninstall" =~ ^[Yy]$ ]]; then
        return
    fi

    print_step "$(msg uninstalling)"

    if [[ -w "$current_path" ]]; then
        rm -f "$current_path" || true
    else
        sudo rm -f "$current_path" || true
    fi

    print_success "$(msg uninstall_done)"

    local install_dir
    install_dir=$(dirname "$current_path")
    print_step "$(msg path_cleanup)"
    cleanup_path_entries "$install_dir"
    print_success "$(msg path_cleanup_done)"

    remove_config_dirs
}

# ============================================================================
# Main
# ============================================================================
# cleanup_tmp removes the download scratch directory however the script exits.
cleanup_tmp() {
    [[ -n "$TMP_WORK_DIR" && -d "$TMP_WORK_DIR" ]] && rm -rf "$TMP_WORK_DIR"
    return 0
}
trap cleanup_tmp EXIT

main() {
    if [[ "$1" == "--uninstall" || "$1" == "uninstall" ]]; then
        UNINSTALL_MODE=true
    fi

    clear
    print_banner
    if [[ "$SKIP_LANG_PROMPT" == "true" ]]; then
        # Respect pre-set LANG_CODE when skipping the language prompt
        LANG_CODE="${LANG_CODE:-en}"
    else
        select_language
    fi

    echo -e "${BOLD}────────────────────────────────────────────${NC}"

    if [[ "$UNINSTALL_MODE" == true ]]; then
        uninstall_client
        exit 0
    fi

    check_os
    check_arch
    check_dependencies
    check_existing_install

    echo ""
    download_binary
    select_install_dir
    install_binary
    ensure_command_access
    update_path
    verify_installation

    # Skip configuration for updates
    if [[ "$IS_UPDATE" != true ]]; then
        configure_client
        test_connection
    else
        echo ""
        local new_version=$(get_version_from_binary "$INSTALL_DIR/$BINARY_NAME")
        print_panel "$(msg update_ok)"
        print_info "Version: $new_version"
        echo ""
        return
    fi

    show_completion
}

# Run
main "$@"
