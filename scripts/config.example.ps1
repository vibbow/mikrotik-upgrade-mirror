# 本地配置模板。复制为 scripts\config.ps1 并填入你自己的信息。
# config.ps1 已加入 .gitignore，不会被提交，所以服务器地址、代理等私人信息不会进 git。
# 被 push-mirror.ps1 和 upgrade-routers.ps1 读取。

# push-mirror.ps1: 把升级包推送到镜像服务器
$PushConfig = @{
    Host      = 'mirror.example.com'              # 镜像服务器 (SSH)
    User      = 'root'
    RemoteDir = '/opt/mikrotik-mirror/packages'
    Proxy     = ''                                # 下载用的代理，如 'http://127.0.0.1:7890'；'' = 不用代理
}

# upgrade-routers.ps1: 批量升级路由器
$UpgradeConfig = @{
    AddressBook = 'C:\path\to\Addresses.cdb'      # Winbox 地址簿 (Winbox 设置里指定的位置)
    Mirror      = '203.0.113.10'                  # 路由器访问镜像服务器用的地址
    IgnoreFile  = 'scripts\ignore.txt'            # 忽略列表 (可选，复制 scripts\ignore.example.txt)
}
