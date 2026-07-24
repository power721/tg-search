package link

import (
	"strings"
	"testing"
)

func TestExtractProviderCorpus(t *testing.T) {
	extractor := NewExtractor()
	cases := []struct {
		name     string
		text     string
		wantType string
		wantURL  string
		wantPass string
	}{
		{"115", "https://115.com/s/abc-123?password=a1B2", "115", "https://115.com/s/abc-123?password=a1B2", "a1B2"},
		{"115 http", "http://115.com/s/abc123?password=a1B2#", "115", "http://115.com/s/abc123?password=a1B2", "a1B2"},
		{"115cdn", "https://115cdn.com/s/share_1", "115", "https://115cdn.com/s/share_1", ""},
		{"anxia", "https://anxia.com/s/share-2 密码: z9", "115", "https://anxia.com/s/share-2", "z9"},
		{"xunlei", "https://pan.xunlei.com/s/VOuNMeKrMwroW9HmY21cZWfPA1?pwd=kewd#", "xunlei", "https://pan.xunlei.com/s/VOuNMeKrMwroW9HmY21cZWfPA1?pwd=kewd", "kewd"},
		{"xunlei http", "http://pan.xunlei.com/s/VOuNMeKrMwroW9HmY21cZWfPA1?pwd=kewd#", "xunlei", "http://pan.xunlei.com/s/VOuNMeKrMwroW9HmY21cZWfPA1?pwd=kewd", "kewd"},
		{"baidu share", "https://pan.baidu.com/s/1Zc_e4792cuvucfI-ZZts0Q?pwd=ruub", "baidu", "https://pan.baidu.com/s/1Zc_e4792cuvucfI-ZZts0Q?pwd=ruub", "ruub"},
		{"baidu http", "http://pan.baidu.com/s/1Zc_e4792cuvucfI-ZZts0Q?pwd=ruub", "baidu", "http://pan.baidu.com/s/1Zc_e4792cuvucfI-ZZts0Q?pwd=ruub", "ruub"},
		{"baidu init", "https://pan.baidu.com/share/init?surl=abc-123&pwd=7788", "baidu", "https://pan.baidu.com/share/init?surl=abc-123&pwd=7788", "7788"},
		{"baidu init extra params", "https://pan.baidu.com/wap/init?surl=abc-123&foo=bar&pwd=7788标签：电影", "baidu", "https://pan.baidu.com/wap/init?surl=abc-123&foo=bar&pwd=7788", "7788"},
		{"pikpak", "https://mypikpak.com/s/Vabc123?pwd=p9", "pikpak", "https://mypikpak.com/s/Vabc123?pwd=p9", "p9"},
		{"pikpak tracking", "https://mypikpak.com/s/Vabc123?pwd=p9&entry=telegram标签：电影", "pikpak", "https://mypikpak.com/s/Vabc123?pwd=p9", "p9"},
		{"tianyi web", "https://cloud.189.cn/web/share?code=AbCd", "tianyi", "https://cloud.189.cn/web/share?code=AbCd", ""},
		{"tianyi encoded t", "https://cloud.189.cn/t/AbCd%E8%AE%BF%E9%97%AE", "tianyi", "https://cloud.189.cn/t/AbCd%E8%AE%BF%E9%97%AE", ""},
		{"tianyi t code", "https://cloud.189.cn/t/AbCd（访问码：7x9q）", "tianyi", "https://cloud.189.cn/t/AbCd", "7x9q"},
		{"tianyi encoded code", "https://cloud.189.cn/t/AbCd%EF%BC%88%E8%AE%BF%E9%97%AE%E7%A0%81%EF%BC%9A7x9q%EF%BC%89", "tianyi", "https://cloud.189.cn/t/AbCd", "7x9q"},
		{"tianyi h5", "https://h5.cloud.189.cn/share.html#/t/AbCd", "tianyi", "https://h5.cloud.189.cn/share.html#/t/AbCd", ""},
		{"mobile caiyun m", "https://caiyun.139.com/m/i?abc123", "mobile", "https://caiyun.139.com/m/i?abc123", ""},
		{"mobile caiyun m www", "https://www.caiyun.139.com/m/i?abc123&foo=bar", "mobile", "https://www.caiyun.139.com/m/i?abc123&foo=bar", ""},
		{"mobile caiyun adjacent label", "https://www.caiyun.139.com/m/i?abc123&foo=bar标签：短剧", "mobile", "https://www.caiyun.139.com/m/i?abc123&foo=bar", ""},
		{"mobile yun shareweb", "https://yun.139.com/shareweb/#/w/i/abc123", "mobile", "https://yun.139.com/shareweb/#/w/i/abc123", ""},
		{"mobile yun shareweb www", "https://www.yun.139.com/shareweb/#/w/i/abc123", "mobile", "https://www.yun.139.com/shareweb/#/w/i/abc123", ""},
		{"mobile caiyun w", "https://caiyun.139.com/w/i/abc123", "mobile", "https://caiyun.139.com/w/i/abc123", ""},
		{"mobile feixin", "https://caiyun.feixin.10086.cn/abc123", "mobile", "https://caiyun.feixin.10086.cn/abc123", ""},
		{"quark", "https://pan.quark.cn/s/8a16ab9c06b9", "quark", "https://pan.quark.cn/s/8a16ab9c06b9", ""},
		{"quark http", "http://pan.quark.cn/s/8a16ab9c06b9", "quark", "http://pan.quark.cn/s/8a16ab9c06b9", ""},
		{"quark adjacent label", "https://pan.quark.cn/s/8a16ab9c06b9标签：电影", "quark", "https://pan.quark.cn/s/8a16ab9c06b9", ""},
		{"uc password", "https://drive.uc.cn/s/d5eaad53?password=xy9z", "uc", "https://drive.uc.cn/s/d5eaad53?password=xy9z", "xy9z"},
		{"uc public", "https://drive.uc.cn/s/d5eaad53da684?public=1", "uc", "https://drive.uc.cn/s/d5eaad53da684?public=1", ""},
		{"uc adjacent password", "https://drive.uc.cn/s/d5eaad53da684?public=1提取码:xy9z", "uc", "https://drive.uc.cn/s/d5eaad53da684?public=1", "xy9z"},
		{"uc fast", "https://fast.uc.cn/s/abc123", "uc", "https://fast.uc.cn/s/abc123", ""},
		{"aliyun folder", "https://www.aliyundrive.com/s/abc123/folder/folder456?password=qwer", "aliyun", "https://www.aliyundrive.com/s/abc123/folder/folder456?password=qwer", "qwer"},
		{"alipan", "https://www.alipan.com/s/MHf34XusdVK", "aliyun", "https://www.alipan.com/s/MHf34XusdVK", ""},
		{"alipan no www", "https://alipan.com/s/MHf34XusdVK", "aliyun", "https://alipan.com/s/MHf34XusdVK", ""},
		{"alipan adjacent label", "https://www.alipan.com/s/MHf34XusdVK标签：电影", "aliyun", "https://www.alipan.com/s/MHf34XusdVK", ""},
		{"123 inline", "https://123pan.com/s/abc123提取码:9a8b", "123", "https://123pan.com/s/abc123", "9a8b"},
		{"123 html", "https://www.123pan.com/s/abc123.html?提取码:9a8b", "123", "https://www.123pan.com/s/abc123.html", "9a8b"},
		{"123 query pwd", "https://www.123pan.com/s/IpPUVv-M1Pdv?pwd=Ocat#", "123", "https://www.123pan.com/s/IpPUVv-M1Pdv", "Ocat"},
		{"123 numeric com", "https://123865.com/s/abc_123", "123", "https://123865.com/s/abc_123", ""},
		{"123 pan cn", "https://www.123pan.cn/s/abc-123?提取码:9a8b", "123", "https://www.123pan.cn/s/abc-123", "9a8b"},
		{"123 adjacent label", "https://www.123pan.cn/s/abc-123标签：短剧", "123", "https://www.123pan.cn/s/abc-123", ""},
		{"123 share pan cn", "https://1850896530.share.123pan.cn/123pan/tSkpvd-K1Ggh?pwd=Zlwl", "123", "https://1850896530.share.123pan.cn/123pan/tSkpvd-K1Ggh?pwd=Zlwl", "Zlwl"},
		{"guangya", "https://www.guangyapan.com/s/ABC_123", "guangya", "https://www.guangyapan.com/s/ABC_123", ""},
		{"magnet", "magnet:?xt=urn:btih:abcdef", "magnet", "magnet:?xt=urn:btih:abcdef", ""},
		{"ed2k", "ed2k://|file|movie.mkv|123|HASH|/", "ed2k", "ed2k://|file|movie.mkv|123|HASH|/", ""},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			links := extractor.Extract(tc.text)
			if len(links) != 1 {
				t.Fatalf("len = %d, want 1: %+v", len(links), links)
			}
			if links[0].Type != tc.wantType || links[0].URL != tc.wantURL || links[0].Password != tc.wantPass {
				t.Fatalf("link = %+v, want type=%s url=%s password=%s", links[0], tc.wantType, tc.wantURL, tc.wantPass)
			}
		})
	}
}

func TestExtractRealMessageCorpus(t *testing.T) {
	text := `海报
名称：2026年6月6日 短剧更新目录12

链接：
🔗 夸克网盘：https://pan.quark.cn/s/8a16ab9c06b9
🔗 百度网盘：https://pan.baidu.com/s/1Zc_e4792cuvucfI-ZZts0Q?pwd=ruub
🔑 提取码：ruub
🔗 UC 网盘：https://drive.uc.cn/s/d5eaad53da684?public=1
🔗 迅雷云盘：https://pan.xunlei.com/s/VOuNMeKrMwroW9HmY21cZWfPA1?pwd=kewd#
🔑 提取码：kewd
🔗 阿里云盘：https://www.alipan.com/s/MHf34XusdVK

🏷 标签：#短剧 #最新短剧 #合集
📢 频道：https://t.me/+Djia5z2lVsI5ODRl
👥 群组：@Quark_Share_Group (https://t.me/Quark_Share_Group)
🤖 投稿：@QuarkRobot (https://t.me/QuarkRobot)`

	links := NewExtractor().Extract(text)
	byType := map[string][]string{}
	for _, item := range links {
		byType[item.Type] = append(byType[item.Type], item.URL)
	}
	want := map[string]string{
		"quark":  "https://pan.quark.cn/s/8a16ab9c06b9",
		"baidu":  "https://pan.baidu.com/s/1Zc_e4792cuvucfI-ZZts0Q?pwd=ruub",
		"uc":     "https://drive.uc.cn/s/d5eaad53da684?public=1",
		"xunlei": "https://pan.xunlei.com/s/VOuNMeKrMwroW9HmY21cZWfPA1?pwd=kewd",
		"aliyun": "https://www.alipan.com/s/MHf34XusdVK",
	}
	for typ, url := range want {
		if !contains(byType[typ], url) {
			t.Fatalf("missing %s %s in links %+v", typ, url, links)
		}
	}
	for _, typ := range []string{"quark", "baidu", "uc", "xunlei", "aliyun"} {
		if len(byType[typ]) != 1 {
			t.Fatalf("type %s count = %d, want 1: %+v", typ, len(byType[typ]), links)
		}
	}
	if len(byType["url"]) != 3 {
		t.Fatalf("fallback url count = %d, want telegram fallback links extracted for rule filtering: %+v", len(byType["url"]), byType["url"])
	}
}

func TestExtractIncludesTelegramLinksForRuleFiltering(t *testing.T) {
	text := `频道：https://t.me/+Djia5z2lVsI5ODRl
群组：http://t.me/Quark_Share_Group
投稿：https://T.ME/QuarkRobot
资源：https://pan.quark.cn/s/abc123
官网：https://example.com/post`

	links := NewExtractor().Extract(text)
	if len(links) != 5 {
		t.Fatalf("len = %d, want telegram and resource links: %+v", len(links), links)
	}
	if links[0].Type != "url" || links[0].URL != "https://t.me/+Djia5z2lVsI5ODRl" {
		t.Fatalf("first link = %+v, want telegram fallback link", links[0])
	}
	if links[3].Type != "quark" || links[3].URL != "https://pan.quark.cn/s/abc123" {
		t.Fatalf("fourth link = %+v, want quark link", links[3])
	}
}

func TestExtractDeduplicatesProviderAndFallback(t *testing.T) {
	links := NewExtractor().Extract("https://pan.quark.cn/s/abc123 https://pan.quark.cn/s/abc123")
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	if links[0].Type != "quark" {
		t.Fatalf("type = %q, want quark", links[0].Type)
	}
}

func TestExtractDoesNotBleedPasswordFromNextLink(t *testing.T) {
	text := "夸克：https://pan.quark.cn/s/8a16ab9c06b9 百度：https://pan.baidu.com/s/1abc?pwd=7788"
	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(links), links)
	}
	if links[0].Type != "quark" || links[0].Password != "" {
		t.Fatalf("first link = %+v, want quark without password copied from baidu", links[0])
	}
	if links[1].Type != "baidu" || links[1].Password != "7788" {
		t.Fatalf("second link = %+v, want baidu password", links[1])
	}
}

func TestExtractAssignsNoteFromTitleBeforeLink(t *testing.T) {
	text := `庆余年 S02 4K 全集
夸克网盘：https://pan.quark.cn/s/abc123

凡人修仙传 最新
阿里云盘：https://www.alipan.com/s/def456`

	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(links), links)
	}
	if links[0].URL != "https://pan.quark.cn/s/abc123" || links[0].Note != "庆余年 S02 4K 全集" {
		t.Fatalf("first link = %+v, want note from preceding title", links[0])
	}
	if links[1].URL != "https://www.alipan.com/s/def456" || links[1].Note != "凡人修仙传 最新" {
		t.Fatalf("second link = %+v, want note from preceding title", links[1])
	}
}

func TestExtractAssignsNoteAcrossLinkLabelLine(t *testing.T) {
	text := `庆余年 S02 4K
链接：
https://pan.quark.cn/s/abc123`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	if links[0].Note != "庆余年 S02 4K" {
		t.Fatalf("note = %q, want title above link label line", links[0].Note)
	}
}

func TestExtractLeavesNoteEmptyForProviderOnlyLabels(t *testing.T) {
	links := NewExtractor().Extract("夸克网盘：https://pan.quark.cn/s/abc123")
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	if links[0].Note != "" {
		t.Fatalf("note = %q, want empty provider label is not a title", links[0].Note)
	}
}

func TestExtractFallbackURLAndFalsePositive(t *testing.T) {
	links := NewExtractor().Extract("官网 https://example.com/a 不是网盘 pan.baidu.com/s/no-scheme")
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	if links[0].Type != "url" || links[0].URL != "https://example.com/a" {
		t.Fatalf("link = %+v, want fallback url", links[0])
	}
}

func TestExtractDoesNotClassifyUnknown123Domain(t *testing.T) {
	links := NewExtractor().Extract("官网 https://123abc.com/s/not-pan")
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1 fallback url: %+v", len(links), links)
	}
	if links[0].Type != "url" {
		t.Fatalf("type = %q, want fallback url", links[0].Type)
	}
}

func TestExtractResourceFields(t *testing.T) {
	text := `资源合集
夸克：https://pan.quark.cn/s/abc123
磁力：magnet:?xt=urn:btih:abcdef
电驴：ed2k://|file|movie.mkv|123|HASH|/
官网：https://example.com/post`

	links := NewExtractor().Extract(text)
	byURL := map[string]struct {
		category string
		snippet  string
	}{}
	for _, item := range links {
		byURL[item.URL] = struct {
			category string
			snippet  string
		}{category: item.Category, snippet: item.SourceSnippet}
	}
	want := map[string]string{
		"https://pan.quark.cn/s/abc123":     "cloud_drive",
		"magnet:?xt=urn:btih:abcdef":        "magnet",
		"ed2k://|file|movie.mkv|123|HASH|/": "ed2k",
		"https://example.com/post":          "http",
	}
	for url, category := range want {
		got, ok := byURL[url]
		if !ok {
			t.Fatalf("missing url %s in %+v", url, links)
		}
		if got.category != category {
			t.Fatalf("category for %s = %q, want %q", url, got.category, category)
		}
		if got.snippet == "" {
			t.Fatalf("source snippet for %s is empty", url)
		}
	}
}

func TestExtractMediaMessageEd2KWithSpaces(t *testing.T) {
	text := `📺 电视剧：斗破苍穹 (2017) - S05E202
🍿 TMDB ID: 79481
⭐️ 评分: 8.2
🎭 类型: 动画,动作冒险,Sci-Fi & Fantasy
📂 分类: 国漫
🎞️ 质量: WEB-DL 2160p
📦 文件: 1 个
💾 大小: 854.19 MB
👥 主演: 刘三木,刘雨轩,万苏婉,鬼月,陈奕雯
📝 简介: 萧炎曾是家族里公认的斗气天才，年仅11岁便已经抵达了常人穷尽一生都无法修炼到的境界。可12岁那年，一场意外让萧炎的全部努力都化为了乌有，失去一切的他体会到了人情的冷暖和世态的炎凉，之后，萧炎和纳兰嫣然...

🔗 链接: 
ed2k://|file|斗破苍穹.2017 - S05E202 - 第 202 集 - 2160p.WEB-DL.SDR.HEVC.AAC 2.0.{tmdb-79481}.mp4|895680618|24E12F6E5868DC08F432B28CDA67172B|/
#国漫`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	wantURL := "ed2k://|file|斗破苍穹.2017 - S05E202 - 第 202 集 - 2160p.WEB-DL.SDR.HEVC.AAC 2.0.{tmdb-79481}.mp4|895680618|24E12F6E5868DC08F432B28CDA67172B|/"
	if links[0].Type != "ed2k" || links[0].URL != wantURL {
		t.Fatalf("link = %+v, want ed2k url %s", links[0], wantURL)
	}
	if links[0].Note != "斗破苍穹 (2017) - S05E202" {
		t.Fatalf("note = %q, want media title", links[0].Note)
	}
}

func TestExtractAssignsMediaMetadataToCloudDriveLinks(t *testing.T) {
	text := `名称：开始推理吧 第四季 (2026)  刘宇宁 金靖 张凌赫 程鑫  综艺  真人秀真人秀  0607期

描述：又名: 开始推理吧 4

链接：
🔗 百度网盘：https://pan.baidu.com/s/1sQSU-e5CoYds6MeFEymS1A?pwd=3345
🔑 提取码：3345
🏷 标签：#刘宇宁 #金靖 #真人秀 #开始推理吧`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "开始推理吧 第四季" {
		t.Fatalf("media title = %q, want 开始推理吧 第四季", link.MediaTitle)
	}
	if link.MediaYear != "2026" || link.MediaEpisode != "0607期" || link.MediaCategory != "综艺" {
		t.Fatalf("metadata = %+v, want year 2026 episode 0607期 category 综艺", link)
	}
	if link.MediaTags != "刘宇宁 金靖 真人秀 开始推理吧" {
		t.Fatalf("tags = %q", link.MediaTags)
	}
}

func TestExtractMediaMetadataSkipsNearbyNoiseBeforeLink(t *testing.T) {
	text := `憨婿
4K S01E01 - E25 HiveWeb

简介：理工高材生韦浩意外魂穿大庸朝
分享：Pluto (https://hdhive.com/user/17888)
大小：2.6GB
链接：直达链接 (https://pan.baidu.com/s/1yHyPAA47gToHykEQfrn3Pw?pwd=t67z)
标签：#憨婿 #剧情`

	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want hdhive fallback and baidu link: %+v", len(links), links)
	}
	link := links[1]
	if link.Type != "baidu" || link.MediaTitle != "憨婿" || link.Note != "憨婿" {
		t.Fatalf("baidu link = %+v, want media title 憨婿", link)
	}
	if link.MediaSeason != "S01" || link.MediaEpisode != "E01" || link.MediaQuality != "4K" || link.MediaSize != "2.6GB" {
		t.Fatalf("metadata = %+v, want season/episode/quality/size", link)
	}
}

func TestExtractMediaMetadataFromStructuredTVMessage(t *testing.T) {
	text := `📺 电视剧：塬上风云 (2026) - S01E30
🍿 TMDB ID: 323346
🎭 类型: 剧情,War & Politics
📂 分类: 国产剧
🎞️ 质量: WEB-DL 2160p HDR10
💾 大小: 3.23 GB

🔗 链接: https://115cdn.com/s/swsznow33xj?password=q474`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "塬上风云" || link.MediaYear != "2026" || link.MediaSeason != "S01" || link.MediaEpisode != "E30" {
		t.Fatalf("title/sequence metadata = %+v", link)
	}
	if link.MediaTMDBID != "323346" || link.MediaCategory != "国产剧" || link.MediaQuality != "WEB-DL 2160p HDR10" || link.MediaSize != "3.23 GB" {
		t.Fatalf("structured metadata = %+v", link)
	}
}

func TestExtractMediaMetadataFromShortDramaTitle(t *testing.T) {
	text := `短剧-完了，这破农场来的全是祖宗第二季（80集）

夸克：https://pan.quark.cn/s/8fd1235933b5
度盘：https://pan.baidu.com/s/1idK69_EZ6stsf5ra1qPwjQ?pwd=3l6e`

	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(links), links)
	}
	for _, link := range links {
		if link.MediaTitle != "完了，这破农场来的全是祖宗第二季" || link.MediaEpisode != "80集" || link.MediaCategory != "短剧" {
			t.Fatalf("link = %+v, want short drama metadata", link)
		}
	}
}

func TestExtractMediaMetadataFromMagnetAndED2KURL(t *testing.T) {
	text := `资源
magnet:?xt=urn:btih:abcdef&dn=维京传奇.2013.S01.2160p.WEB-DL.mkv
ed2k://|file|刀.1995.USA.UHD.Blu-ray.2160p.DV.HDR.mkv|94070593331|ABCDEF0123456789|/`

	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(links), links)
	}
	if links[0].MediaTitle != "维京传奇.2013.S01.2160p.WEB-DL" || links[0].MediaYear != "2013" || links[0].MediaSeason != "S01" {
		t.Fatalf("magnet metadata = %+v", links[0])
	}
	if links[1].MediaTitle != "刀.1995.USA.UHD.Blu-ray.2160p.DV.HDR" || links[1].MediaYear != "1995" || links[1].MediaQuality == "" || links[1].MediaSize == "" {
		t.Fatalf("ed2k metadata = %+v", links[1])
	}
}

func TestExtractMediaMetadataFromResourceNameAndPikPak(t *testing.T) {
	text := `资源名称：圆桌派

描述：圆桌派全季全集，《圆桌派》，别名《圆桌π》是一档的聊天文化类网络电视节目。

🧲 链接: https://mypikpak.com/s/VO

👉使用 PikPak 秒存，立即在线观看👈 (https://toapp.mypikpak.com/toapp?__add_url=https://mypikpak.com/s/VO&source=pptg&__campaign=/s/VO)

📁 文件大小：86.7GB
🏷 文件类型：#脱口秀#综艺##文化节目#中国文化`

	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want pikpak resource and toapp fallback for rule filtering: %+v", len(links), links)
	}
	link := links[0]
	if link.Type != "pikpak" || link.URL != "https://mypikpak.com/s/VO" {
		t.Fatalf("link = %+v, want pikpak resource", link)
	}
	if link.MediaTitle != "圆桌派" || link.Note != "圆桌派" || link.MediaSize != "86.7GB" || link.MediaCategory != "综艺" {
		t.Fatalf("metadata = %+v, want title/size/category", link)
	}
	if link.MediaTags != "脱口秀 综艺 文化节目 中国文化" {
		t.Fatalf("tags = %q", link.MediaTags)
	}
}

func TestExtractMediaMetadataFromPlainTVTitle(t *testing.T) {
	text := `电视剧 超感迷宫 2025 4K 全20集
链接：https://cloud.189.cn/t/Y7rUvynue6vm`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "超感迷宫" || link.MediaYear != "2025" || link.MediaQuality != "4K" || link.MediaEpisode != "20集" || link.MediaCategory != "电视剧" {
		t.Fatalf("metadata = %+v, want plain tv title metadata", link)
	}
}

func TestExtractMediaMetadataFromAngleBracketCategory(t *testing.T) {
	text := `《国产剧》迷墙 (2026)  2160p WEB-DL H265 DDP5.1 主演: 郭京飞 / 任素汐 / 谷嘉诚 / 漆昱辰 / 温峥嵘

123云盘：https://1856557151.share.123pan.cn/123pan/oJqrvd-YON9d

百度网盘：https://pan.baidu.com/s/184SrcMA15CApBQsvjtLFVQ?pwd=x7hv`

	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want 2: %+v", len(links), links)
	}
	for _, link := range links {
		if link.MediaTitle != "迷墙" || link.MediaYear != "2026" || link.MediaCategory != "国产剧" {
			t.Fatalf("link = %+v, want title/year/category", link)
		}
		if link.MediaQuality != "2160p WEB-DL H265 DDP5.1" {
			t.Fatalf("quality = %q", link.MediaQuality)
		}
	}
}

func TestExtractMediaMetadataFromInlineDriveLinksAndTags(t *testing.T) {
	text := `海贼王合集 国语日语

◎年  代 1999
◎产  地 日本
◎类  别 喜剧 / 动作 / 动画 / 奇幻 / 冒险
◎豆  瓣 9.5

大小：1.5T
标签：#海贼王 #航海王 #ワンピース #OnePiece #动画 #动漫 #爷青回 阿里   https://www.aliyundrive.com/s/QyVTWdmGM1o 115     https://115cdn.com/s/swfx55h3ffc?password=s367#
访问码：s367`

	links := NewExtractor().Extract(text)
	if len(links) != 2 {
		t.Fatalf("len = %d, want aliyun and 115 links: %+v", len(links), links)
	}
	for _, link := range links {
		if link.MediaTitle != "海贼王合集 国语日语" || link.MediaYear != "1999" || link.MediaSize != "1.5T" {
			t.Fatalf("link = %+v, want title/year/size", link)
		}
		if link.MediaTags != "海贼王 航海王 ワンピース OnePiece 动画 动漫 爷青回 阿里" {
			t.Fatalf("tags = %q", link.MediaTags)
		}
	}
	if links[1].Type != "115" || links[1].Password != "s367" {
		t.Fatalf("second link = %+v, want 115 password", links[1])
	}
}

func TestExtractMediaMetadataFromShortDramaDirectory(t *testing.T) {
	text := `名称：2026年6月9日 短剧更新目录3

描述：目录：
1.白绫三尺惜红颜（63集）吴竹照＆觅七
2.嫡女归京，我被狼群宠上天（73集）Ai短剧

阿里：https://www.alipan.com/s/TTXfbCYaCgk
夸克：https://pan.quark.cn/s/689d3b4512f2
百度：https://pan.baidu.com/s/1eKVTQkEETVy1hIYS8YA5-A?pwd=tjyd

📁 大小：N
🏷 标签：#短剧 #最新短剧 #合集 #擦边短剧 #短剧榜 #热力榜`

	links := NewExtractor().Extract(text)
	if len(links) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(links), links)
	}
	for _, link := range links {
		if link.MediaTitle != "2026年6月9日 短剧更新目录3" || link.MediaCategory != "短剧" {
			t.Fatalf("link = %+v, want directory metadata", link)
		}
		if link.MediaSize != "" {
			t.Fatalf("media size = %q, want invalid size ignored", link.MediaSize)
		}
		if link.MediaTags != "短剧 最新短剧 合集 擦边短剧 短剧榜 热力榜" {
			t.Fatalf("tags = %q", link.MediaTags)
		}
	}
	if links[2].Type != "baidu" || links[2].Password != "tjyd" {
		t.Fatalf("baidu link = %+v, want password", links[2])
	}
}

func TestExtractMediaMetadataFromUpdatedEpisodeTitle(t *testing.T) {
	text := `名称：迷墙 (2026) 更至06集 [4K][剧情][郭京飞/任素汐]

描述：倒霉透顶的小夫妻。

链接：https://pan.xunlei.com/s/VOuXNeFlYfJVnesX3zRR8IRiA1?pwd=3ypd#

📁 大小：NG
🏷 标签：#迷墙 #剧集 #4K #剧情 #郭京飞 #任素汐 #xunlei`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "迷墙" || link.MediaYear != "2026" || link.MediaEpisode != "更新06集" || link.MediaQuality != "4K" {
		t.Fatalf("metadata = %+v, want updated episode metadata", link)
	}
	if link.MediaSize != "" {
		t.Fatalf("media size = %q, want invalid size ignored", link.MediaSize)
	}
}

func TestExtractMediaMetadataFromEmojiMovieTitleWithBracketDetails(t *testing.T) {
	text := `📅 6月10日

🎬 ✅《迷墙（2026）》【更新至 08集】【4K.SDR.60fps】【国语】【中文字幕】【单集：1.5G】

类型：国产剧
分享：星空影视分享
💾 网盘：百度网盘

📝 简介：
倒霉透顶的小夫妻，盲打误撞住进了一幢大别墅，意外发现了一笔藏匿其中的巨款，风波袭来，让他们分不清，这到底是噩梦还是美梦。

https://pan.baidu.com/s/10dWJ_mLAqiBruTnSxFjrow?pwd=1111`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.Type != "baidu" || link.URL != "https://pan.baidu.com/s/10dWJ_mLAqiBruTnSxFjrow?pwd=1111" || link.Password != "1111" {
		t.Fatalf("link = %+v, want baidu URL with password", link)
	}
	if link.MediaTitle != "迷墙" || link.Note != "迷墙" {
		t.Fatalf("title/note = %q/%q, want 迷墙", link.MediaTitle, link.Note)
	}
	if link.MediaYear != "2026" || link.MediaEpisode != "更新08集" || link.MediaQuality != "4K SDR 60fps" || link.MediaCategory != "国产剧" {
		t.Fatalf("metadata = %+v, want year/update/quality/category", link)
	}
}

func TestExtractMediaMetadataKeepsHeaderTitleWithProviderLabelOnLinkLine(t *testing.T) {
	text := `🗄 速度与激情9 F9: The Fast Saga (2021)【4K SDR 无损超清】

📜介绍：
"唐老大"多姆·托莱多（范·迪塞尔 饰）与莱蒂（米歇尔·罗德里格兹 饰）和他的儿子小布莱恩，过上了远离纷扰的平静生活。

链接
光鸭：https://www.guangyapan.com/s/1927300986124070927_alzQ-sdmlFrewcK7

📁 大小：N
🏷 标签：#速度与激情9 #leoziyuan #F9狂野时速(港)#玩命关头9#动作#犯罪`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.Type != "guangya" || link.URL != "https://www.guangyapan.com/s/1927300986124070927_alzQ-sdmlFrewcK7" {
		t.Fatalf("link = %+v, want guangya URL", link)
	}
	if link.MediaTitle != "速度与激情9 F9: The Fast Saga" {
		t.Fatalf("media title = %q, want title from 🗄 header line, not provider label", link.MediaTitle)
	}
	if link.Note != "光鸭" {
		t.Fatalf("note = %q, want provider label 光鸭", link.Note)
	}
	if link.MediaYear != "2021" || link.MediaQuality != "4K SDR" {
		t.Fatalf("metadata = %+v, want year 2021 and quality 4K SDR from header line", link)
	}
	if link.MediaTags != "速度与激情9 leoziyuan F9狂野时速(港) 玩命关头9 动作 犯罪" {
		t.Fatalf("tags = %q, want tags from 🏷 line", link.MediaTags)
	}
}

func TestExtractMediaMetadataFromOneMessageMultipleTianyiTitles(t *testing.T) {
	text := `日剧分享六
麻烦一族.Involvement in Family Affairs.(2022) {tmdb-158896}
链接：https://cloud.189.cn/t/q2yUJjR7Bjqm（访问码：5zk9）
罗布奥特曼.Ultraman R／B.(2018) {tmdb-81959}
链接：https://cloud.189.cn/t/2Q3Aban67fii（访问码：xmt5）
恋爱何必认真？.What Do You Really Do About Love？.(2022) {tmdb-194854}
链接：https://cloud.189.cn/t/aEnIBjaUbUVj（访问码：1wbv）

标签  #剧集 #合集 #刮销 #4k

大小：1t`

	links := NewExtractor().Extract(text)
	if len(links) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(links), links)
	}
	want := []struct {
		title string
		year  string
		tmdb  string
		pass  string
	}{
		{"麻烦一族.Involvement in Family Affairs.", "2022", "158896", "5zk9"},
		{"罗布奥特曼.Ultraman R／B.", "2018", "81959", "xmt5"},
		{"恋爱何必认真？.What Do You Really Do About Love？.", "2022", "194854", "1wbv"},
	}
	for i, item := range want {
		if links[i].MediaTitle != item.title || links[i].MediaYear != item.year || links[i].MediaTMDBID != item.tmdb || links[i].Password != item.pass {
			t.Fatalf("link %d = %+v, want %+v", i, links[i], item)
		}
		if links[i].MediaSize != "1t" || links[i].MediaTags != "剧集 合集 刮销 4k" {
			t.Fatalf("shared metadata for link %d = %+v", i, links[i])
		}
	}
}

func TestExtractMediaMetadataFromBracketQualityAndSeasonRanges(t *testing.T) {
	text := `名称：厂区日志（2026）【4K.HDR.50fps】【更12集】【剧情/喜剧】【王宁/尹贝希】
.
描述：在大城市工作的王美琳和唐甜。
.
链接：https://pan.quark.cn/s/096c12ad4222
.
📁 大小：NG
🏷 标签：#国剧 #剧情 #喜剧 #厂区日志 #4K #HDR #50fps`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "厂区日志" || link.MediaYear != "2026" || link.MediaEpisode != "更新12集" {
		t.Fatalf("metadata = %+v, want title/year/update episode", link)
	}
	if link.MediaQuality != "4K HDR 50fps" {
		t.Fatalf("quality = %q", link.MediaQuality)
	}
}

func TestExtractMediaMetadataFromStructuredTVWithBracketQuality(t *testing.T) {
	text := `📺 电视剧：莫离 (2026) S01E01
🍿 TMDB ID: 292696
⭐️ 评分: 0.0
🎭 题材: 剧情
📂 地区: 大陆
🎞️ 质量: [4K] [HDR10]
💾 大小: 2.33 GB
🔗 链接: https://115.com/s/swszh9233xj?password=q8e8`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "莫离" || link.MediaYear != "2026" || link.MediaSeason != "S01" || link.MediaEpisode != "E01" {
		t.Fatalf("title/sequence metadata = %+v", link)
	}
	if link.MediaQuality != "[4K] [HDR10]" || link.MediaSize != "2.33 GB" || link.MediaTMDBID != "292696" {
		t.Fatalf("structured metadata = %+v", link)
	}
}

func TestExtractMediaMetadataFromLatestShortDramaMobileShare(t *testing.T) {
	text := `最新擦边短剧：伪装的爱&疯狂试爱&情牵梦绕&危险同居&红唇温差&夜海沉沦&魂牵旧梦&深情蚀骨&秘爱成瘾&她影缠心（完整版）擦边短剧

描述：喜欢就存 速存不补

链接：https://yun.139.com/shareweb/#/w/i/2v3Ez1bGGYnpm

📁 大小：N
🏷 标签：#短剧 #最新短剧 #合集 #擦边短剧 #短剧榜 #热力榜`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.Type != "mobile" || link.URL != "https://yun.139.com/shareweb/#/w/i/2v3Ez1bGGYnpm" {
		t.Fatalf("link = %+v, want mobile yun share", link)
	}
	if link.MediaTitle != "伪装的爱&疯狂试爱&情牵梦绕&危险同居&红唇温差&夜海沉沦&魂牵旧梦&深情蚀骨&秘爱成瘾&她影缠心" || link.MediaCategory != "短剧" {
		t.Fatalf("metadata = %+v, want short drama title/category", link)
	}
	if link.MediaSize != "" || link.MediaTags != "短剧 最新短剧 合集 擦边短剧 短剧榜 热力榜" {
		t.Fatalf("size/tags = size:%q tags:%q", link.MediaSize, link.MediaTags)
	}
}

func TestExtractMediaMetadataFromMovieLabelAndBareMobileLink(t *testing.T) {
	text := `电影：星河入梦 (2026) 王鹤棣 / 宋茜

剧情：近未来，虚拟梦境系统“良梦”问世。

链接：
https://yun.139.com/shareweb/#/w/i/2uR1qyBjPmJnp

🏷：#夸克网盘 #百度网盘 #迅雷网盘 #UC网盘 #电影`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.Type != "mobile" || link.MediaTitle != "星河入梦" || link.MediaYear != "2026" || link.MediaCategory != "电影" {
		t.Fatalf("metadata = %+v, want movie mobile metadata", link)
	}
	if link.MediaTags != "夸克网盘 百度网盘 迅雷网盘 UC网盘 电影" {
		t.Fatalf("tags = %q", link.MediaTags)
	}
}

func TestExtractMediaMetadataFromMultiProviderUpdatedAnime(t *testing.T) {
	text := `名称：绝世战魂  更新至181集  4K
.
描述：四大宗门之首的玄灵宗。
.
UC：https://drive.uc.cn/s/ea0bc2d5c64e4?public=1
夸克：https://pan.quark.cn/s/55e3d0a4e4cf
百度：https://pan.baidu.com/s/1Sd0rWph5mLJFsVD6eAV5uA?pwd=yyds
迅雷：https://pan.xunlei.com/s/VOu5JoqaMd-LhqsmowoScmfEA1?pwd=qaah

🏷 标签：#绝世战魂 #多多影音 #ucquark #baidu #xunlei`

	links := NewExtractor().Extract(text)
	if len(links) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(links), links)
	}
	for _, link := range links {
		if link.MediaTitle != "绝世战魂" || link.MediaEpisode != "更新181集" || link.MediaQuality != "4K" {
			t.Fatalf("link = %+v, want title/update/quality", link)
		}
	}
	if links[2].Password != "yyds" || links[3].Password != "qaah" {
		t.Fatalf("passwords = baidu:%q xunlei:%q", links[2].Password, links[3].Password)
	}
}

func TestExtractMediaMetadataFromDetailedMovieQualityLines(t *testing.T) {
	text := `🎬 真人快打2 (2026)

🎭 类型：电影
⭐️ TMDB评分：7.5/10
🖥 画质：1080p
📹 视频：WEB-DL.H.264
📦 大小：48.37GB

1080P&4K WEB-DL DV/HDR/SDR DDP5.1 [英语][内封简繁字幕]

🔗 链接：123网盘 (https://www.123pan.com/s/IpPUVv-M1Pdv?pwd=Ocat#)

🏷 标签：#真人快打2 #动作 #奇幻 #冒险`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "真人快打2" || link.MediaYear != "2026" || link.MediaCategory != "电影" || link.MediaSize != "48.37GB" {
		t.Fatalf("metadata = %+v, want movie fields", link)
	}
	for _, token := range []string{"1080p", "WEB-DL.H.264", "4K", "DV", "HDR", "SDR", "DDP5.1"} {
		if !strings.Contains(link.MediaQuality, token) {
			t.Fatalf("quality = %q, missing %s", link.MediaQuality, token)
		}
	}
}

func TestExtractMediaMetadataSkipsStandaloneDateHeading(t *testing.T) {
	text := `📅 6月9日

🎬 红色珍珠（2026）更新62集

类型：韩国剧
分享：123panHH
💾 网盘：123网盘

📝 简介：
【SPOTV新闻=姜孝真记者】演员朴真熙将正式展开回归活动。7日，据SPOTV新闻采访，朴真熙将出演KBS新日日剧《红珍珠》的主人公。

🔗 链接：https://www.123pan.com/s/IpPUVv-M1Pdv?pwd=Ocat#`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "红色珍珠" || link.Note != "红色珍珠" {
		t.Fatalf("title/note = %q/%q, want 红色珍珠", link.MediaTitle, link.Note)
	}
	if link.MediaYear != "2026" || link.MediaEpisode != "更新62集" || link.MediaCategory != "韩国剧" {
		t.Fatalf("metadata = %+v, want year/category/update episode", link)
	}
}

func TestExtractMediaMetadataFromUpdateNotificationStatus(t *testing.T) {
	text := `📺 影片更新通知~

电视剧名：豺狼的日子 (2024)
类型：#剧情 #动作冒险 #悬疑
季数：第1季
地区：#英国
平台：#Sky_Atlantic #Sky_Showcase

状态：更新至第2集(共10集)

🔗 123盘地址：https://www.123pan.com/s/dU7jjv-cqUHA`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want 1: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "豺狼的日子" || link.MediaYear != "2024" || link.MediaSeason != "第1季" || link.MediaEpisode != "更新2集" {
		t.Fatalf("metadata = %+v, want tv update status", link)
	}
	if link.MediaCategory != "#剧情 #动作冒险 #悬疑" {
		t.Fatalf("category = %q", link.MediaCategory)
	}
}

func TestExtractMediaMetadataFromHDHiveResourceURL(t *testing.T) {
	text := `白日飞升 (2026)
S01E01-E08 4K SDR 内嵌简中 DDP.2.0 HiveWeb

简介：万古仙王苏无病与上仙林宁雪历经三百年等待。

分享：任秋叶卷起 (https://hdhive.com/user/31485)
大小：1.59KB｜400MB/集
链接：直达链接 (https://hdhive.com/resource/f3ffec8b2d394724a0b43aecaff1e5c0)
网址：白日飞升 (2026) (https://hdhive.com/tv/e78e028399154625934aaa6a34515060)

标签：#白日飞升 #剧情`

	links := NewExtractor().Extract(text)
	if len(links) != 3 {
		t.Fatalf("len = %d, want user/resource/tv fallback URLs: %+v", len(links), links)
	}
	resource := links[1]
	if resource.Type != "url" || resource.URL != "https://hdhive.com/resource/f3ffec8b2d394724a0b43aecaff1e5c0" {
		t.Fatalf("resource link = %+v", resource)
	}
	if resource.MediaTitle != "白日飞升" || resource.MediaYear != "2026" || resource.MediaSeason != "S01" || resource.MediaEpisode != "E01" {
		t.Fatalf("metadata = %+v, want hdhive title/season", resource)
	}
	if resource.MediaQuality != "4K SDR DDP.2.0" || resource.MediaSize != "1.59KB" {
		t.Fatalf("quality/size = %q/%q", resource.MediaQuality, resource.MediaSize)
	}
}

func TestExtractIncludesCollectionReferenceLinksForRuleFiltering(t *testing.T) {
	text := `🎥 迈克尔·杰克逊：巨星之路 (2026)

🗂 所属合集：Michael Collection (https://telegra.ph/Michael-Collection-06-09)
⭐️ 评分：7.7
🏷 类型：音乐 / 剧情
🔖 标签: #迈克尔杰克逊巨星之路 #电影 #22.61G #4K #DDP #DV #HEVC #WEB-DL #MichaelCollection`

	links := NewExtractor().Extract(text)
	if len(links) != 1 || links[0].URL != "https://telegra.ph/Michael-Collection-06-09" {
		t.Fatalf("links = %+v, want collection reference extracted for rule filtering", links)
	}
}

func TestExtractMediaMetadataFromHDHiveBracketSeries(t *testing.T) {
	text := `[剧集] 神探狄仁杰 (2004)
S01 4K WEB-DL HQ HiveWeb 补E27

简介：《神探狄仁杰》系列是由钱雁秋执导。

分享：白可乐 (https://hdhive.com/user/2)
TMDB: 44277 (https://www.themoviedb.org/tv/44277)
大小：124.14GB
字幕：内嵌简中

直达链接 (https://hdhive.com/resource/115/01e82030dcab468697a8113343684ff9) ｜ 神探狄仁杰 (https://hdhive.com/tv/30bbc00cfa1811ed91ff0242c0a81003)

标签：#神探狄仁杰 #剧情 #悬疑`

	links := NewExtractor().Extract(text)
	if len(links) != 4 {
		t.Fatalf("len = %d, want user/TMDB/resource/tv fallback URLs: %+v", len(links), links)
	}
	resource := links[2]
	if resource.URL != "https://hdhive.com/resource/115/01e82030dcab468697a8113343684ff9" || resource.MediaTitle != "神探狄仁杰" {
		t.Fatalf("resource = %+v", resource)
	}
	if resource.MediaYear != "2004" || resource.MediaSeason != "S01" || resource.MediaEpisode != "补E27" || resource.MediaSize != "124.14GB" {
		t.Fatalf("metadata = %+v", resource)
	}
	if resource.MediaQuality != "4K WEB-DL" || resource.MediaCategory != "剧集" {
		t.Fatalf("quality/category = %q/%q", resource.MediaQuality, resource.MediaCategory)
	}
}

func TestExtractMediaMetadataFromMultiProviderMovieQualityTags(t *testing.T) {
	text := `名称：迈克尔·杰克逊：巨星之路(2026)【4K.HDR10+】【高码率】【内封简繁英】【杜比全景声】

描述：影片讲述迈克尔·杰克逊在音乐之外的人生旅程。

夸克：https://pan.quark.cn/s/bc50d367da9f
百度：https://pan.baidu.com/s/1Fpy0M0UecRQE0On6gZ3KUQ?pwd=Yu66
迅雷：https://pan.xunlei.com/s/VOuermFOuYbi6feIHIIccebqA1?pwd=y2fz
115：https://115cdn.com/s/swsznst3wwq?password=m2f2

📁 大小：22.5GB
🏷 标签：#迈克尔杰克逊 #巨星之路 #4K #HDR10 #高码率 #杜比全景声`

	links := NewExtractor().Extract(text)
	if len(links) != 4 {
		t.Fatalf("len = %d, want 4: %+v", len(links), links)
	}
	for _, link := range links {
		if link.MediaTitle != "迈克尔·杰克逊：巨星之路" || link.MediaYear != "2026" || link.MediaSize != "22.5GB" {
			t.Fatalf("link = %+v, want movie metadata", link)
		}
		for _, token := range []string{"4K", "HDR10", "高码率", "杜比全景声"} {
			if !strings.Contains(link.MediaQuality, token) {
				t.Fatalf("quality = %q, missing %s", link.MediaQuality, token)
			}
		}
	}
	if links[1].Password != "Yu66" || links[2].Password != "y2fz" || links[3].Password != "m2f2" {
		t.Fatalf("passwords = %+v", links)
	}
}

func TestExtractMediaMetadataFromAudioDramaMobileShare(t *testing.T) {
	text := `名称：多人有声剧《无限辉煌图卷》主播：格蕾丝语 1507集完

描述：神道魔法百界族，异能武斗狂歌度。

链接：https://yun.139.com/shareweb/#/w/i/2v3EDrw9AeH0c

📁 大小：12.9G
🏷 标签：#有声书 #温茶米酒 #多人有声剧 #无限辉煌图卷 #格蕾丝语 #移动`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want mobile link: %+v", len(links), links)
	}
	link := links[0]
	if link.Type != "mobile" || link.MediaTitle != "多人有声剧《无限辉煌图卷》主播：格蕾丝语" || link.MediaEpisode != "1507集" {
		t.Fatalf("metadata = %+v, want audio drama title/episodes", link)
	}
	if link.MediaSize != "12.9G" || link.MediaTags != "有声书 温茶米酒 多人有声剧 无限辉煌图卷 格蕾丝语 移动" {
		t.Fatalf("size/tags = %q/%q", link.MediaSize, link.MediaTags)
	}
}

func TestExtractMediaMetadataFromQuarkEmojiTitleWith60FPS(t *testing.T) {
	text := `🗄 莫离（2026）【4K.HDR.60fps】【内封简中】【更05集】【爱情/古装】【白鹿/丞磊】

📜介绍：
叶府的长女叶璃，嫁去破败的定王府。

💾夸克网盘 (https://pan.quark.cn/s/8b6e84fde0a5)

📁 大小：NG
🏷 标签：#国剧 #leoziyuan #爱情 #古装 #莫离 #4K #HDR #60fps`

	links := NewExtractor().Extract(text)
	if len(links) != 1 {
		t.Fatalf("len = %d, want quark link: %+v", len(links), links)
	}
	link := links[0]
	if link.MediaTitle != "莫离" || link.MediaYear != "2026" || link.MediaEpisode != "更新05集" {
		t.Fatalf("metadata = %+v, want title/year/update episode", link)
	}
	if link.MediaQuality != "4K HDR 60fps" {
		t.Fatalf("quality = %q", link.MediaQuality)
	}
}

func TestExtractMediaMetadataFromShortDramaMultipleLinks(t *testing.T) {
	text := `名称：凡人百世书第二季 (94集) | 短剧

描述：2026年06月09日最新热门抖音快手百度番茄红果等付费短剧推荐。

链接：https://pan.quark.cn/s/72d151cb5d18
https://pan.baidu.com/s/1bXhVcHMoVDhLZmfs0Q49Hg?pwd=8888
https://drive.uc.cn/s/b3aa070e16134

📁 大小：2.1 GB
🏷 标签：#凡人百世书第二季 #短剧`

	links := NewExtractor().Extract(text)
	if len(links) != 3 {
		t.Fatalf("len = %d, want 3: %+v", len(links), links)
	}
	for _, link := range links {
		if link.MediaTitle != "凡人百世书第二季" || link.MediaEpisode != "94集" || link.MediaCategory != "短剧" || link.MediaSize != "2.1 GB" {
			t.Fatalf("link = %+v, want short drama metadata", link)
		}
	}
	if links[1].Password != "8888" {
		t.Fatalf("baidu password = %q", links[1].Password)
	}
}

func TestExtractMediaMetadataFromYunpansSearchResultList(t *testing.T) {
	text := `夸克频道 新地址  @yunpans

🔍 搜索结果：
·难哄 (2025) [全32集] [1080P] [内封简繁] [白敬亭/章若楠] (https://pan.quark.cn/s/dba77b4d5c05)·quark
·难哄 (2025) (https://pan.quark.cn/s/fdcd408b8669)·quark
·难哄(2025)【4K.HDR.60fps】【内封简中】【32集全】【剧情/爱情】【白敬亭/章若楠】 (https://pan.quark.cn/s/0ac9dcdecaf3)·quark
·难哄 (2025) 4K  全 32集 已完结 易河蟹 速存 (https://pan.xunlei.com/s/VOL-MK-zedh7csmMmR9lE6qpA1?pwd=ccfm)·xunlei
·难哄 (2025) 【更新EP32 1080p/4K】完结【白敬亭/章若楠/爱情】附小说漫画广播剧 (https://pan.baidu.com/s/1G43Mt58j0G5geocnx5U2-Q?pwd=9f41)·baidu
·难哄(2025)4K 10bit 60FPS S01完结 (https://www.123865.com/s/L4qVTd-Tmbud)·123pan
·难哄  漫剧2季 (https://pan.quark.cn/s/5ab3bf941a66)·quark
·最新短剧 2025年4月21日合集目录1 (https://pan.baidu.com/s/19OvoVqFxKquWChqFmlOrUA?pwd=g7mt)·baidu
·最新短剧 2025年4月21日合集目录1 (https://drive.uc.cn/s/9ac00dfc5f864?public=1)·uc
·最新短剧 2025年4月21日合集目录1 (https://pan.quark.cn/s/25102f853785)·quark
·2025.4.8最新短剧合集总链目录1 (https://drive.uc.cn/s/6f8f64e4bc424?public=1)·uc
·2025.4.8最新短剧合集总链目录1 (https://pan.baidu.com/s/133FiVN7nWs73EcqAxJ5NBw?pwd=td2s)·baidu
·2025.4.8最新短剧合集总链目录1 (https://pan.quark.cn/s/131d160dc035)·quark
·伪骨科短剧大合集（45部） (https://pan.quark.cn/s/7be6de8d6b8a)·quark
·双胎小孕妻超难哄（64集）王阡惠＆张北淅 (https://pan.baidu.com/s/1bCQZBSSv9iEtMKb-GGZhvQ?pwd=s4ex)·baidu
·双胎小孕妻超难哄（64集）王阡惠＆张北淅 (https://pan.quark.cn/s/c589040499a0)·quark
·他的小难哄 (https://pan.quark.cn/s/54ce1f9fcae4)·quark

失效资源反馈命令：/f 网盘链接 ，有效反馈可以增加你的频道使用积分，使用 /me 命令可以查看自己的积分。`

	links := NewExtractor().Extract(text)
	if len(links) != 17 {
		t.Fatalf("len = %d, want 17: %+v", len(links), links)
	}
	wantFirst := []struct {
		typ     string
		url     string
		pass    string
		quality string
	}{
		{"quark", "https://pan.quark.cn/s/dba77b4d5c05", "", "1080P"},
		{"quark", "https://pan.quark.cn/s/fdcd408b8669", "", ""},
		{"quark", "https://pan.quark.cn/s/0ac9dcdecaf3", "", "4K HDR 60fps"},
		{"xunlei", "https://pan.xunlei.com/s/VOL-MK-zedh7csmMmR9lE6qpA1?pwd=ccfm", "ccfm", "4K"},
		{"baidu", "https://pan.baidu.com/s/1G43Mt58j0G5geocnx5U2-Q?pwd=9f41", "9f41", "1080p 4K"},
		{"123", "https://www.123865.com/s/L4qVTd-Tmbud", "", "4K 60FPS"},
	}
	for i, want := range wantFirst {
		link := links[i]
		if link.Type != want.typ || link.URL != want.url || link.Password != want.pass {
			t.Fatalf("link %d = %+v, want type=%s url=%s pass=%s", i, link, want.typ, want.url, want.pass)
		}
		if link.MediaTitle != "难哄" || link.MediaYear != "2025" {
			t.Fatalf("link %d metadata = %+v, want title 难哄 year 2025", i, link)
		}
		if want.quality != "" && link.MediaQuality != want.quality {
			t.Fatalf("link %d quality = %q, want %q", i, link.MediaQuality, want.quality)
		}
		if strings.Contains(link.MediaTitle, "yunpans") || strings.Contains(link.Note, "yunpans") {
			t.Fatalf("link %d inherited channel announcement: %+v", i, link)
		}
	}
	if links[0].MediaEpisode != "32集" || links[2].MediaEpisode != "32集" || links[3].MediaEpisode != "32集" {
		t.Fatalf("episodes = %q/%q/%q, want 32集", links[0].MediaEpisode, links[2].MediaEpisode, links[3].MediaEpisode)
	}
}

func contains(items []string, want string) bool {
	for _, item := range items {
		if item == want {
			return true
		}
	}
	return false
}
