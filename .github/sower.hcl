remote {
    type = "sower"
    addr = "proxy.com"
    password = "I_am_Passw0rd"
    # type="socks5"
    # addr="127.0.0.1:7890"
}

dns {
    serve = "127.0.0.1"
    fallback = "223.5.5.5"
}

socks_5 {
    addr = "127.0.0.1:1080"
}

router "block" {
    file = "https://raw.githubusercontent.com/pexcn/daily/gh-pages/adlist/adlist.txt"
    rules = []
}

router "direct" {
    file = "https://raw.githubusercontent.com/pexcn/daily/gh-pages/chinalist/chinalist.txt"
    rules = [
      "imap.*.*",
      "imap.*.*.*",
      "smtp.*.*",
      "smtp.*.*.*",
      "pop.*.*",
      "pop.*.*.*",
      "pop3.*.*",
      "pop3.*.*.*",
      "**.cn",
      ]
}

router "proxy" {
    file = "https://raw.githubusercontent.com/pexcn/daily/gh-pages/gfwlist/gfwlist.txt"
    rules = [
      "**.google.*",
      "**.goo.gl",
      "**.googleusercontent.com",
      "**.googleapis.com",
      "*.googlesource.com",
      "**.youtube.com",
      "**.ytimg.com",
      "**.ggpht.com",
      "**.googlevideo.com",
      "**.facebook.com",
      "**.fbcdn.net",
      "**.twitter.com",
      "**.twimg.com",
      "**.blogspot.com",
      "**.appspot.com",
      "**.wikipedia.org",
      "**.wikimedia.org",
      "*.cloudfront.net",
      "**.amazon.com",
      "**.amazonaws.com",
      "*.githubusercontent.com",
      "*.githubassets.com",
      "*.github.*",
      "lookup-api.apple.com",
    ]
}

router "country" {
    mmdb = "Country.mmdb"  # https://github.com/alecthw/mmdb_china_ip_list
    file = "https://raw.githubusercontent.com/pexcn/daily/gh-pages/chnroute/chnroute.txt"
    rules = [
        "127.0.0.0/8",
        "172.16.0.0/12",
        "192.168.0.0/16",
        "10.0.0.0/8",
        "17.0.0.0/8",
        "100.64.0.0/10",
        "224.0.0.0/4",
        "fe80::/10",
    ]
}