package aigate

import (
	"testing"
)

func TestMemoryLayers(t *testing.T) {
	e := newEnv(t, Options{})
	g := e.g
	// a note from before kinds existed reads as profile
	if err := g.st.PutSetting(notesSetting, `[{"id":1,"ts":1,"text":"杠杆偏好：多空都用 8 倍","by":"owner"}]`); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveNote("在等 SOL 回踩 140 再开多", "ai", "plan", []string{"SOL"}, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveNote("不碰 meme 币和新上线的币", "ai", "profile", nil, 0); err != nil {
		t.Fatal(err)
	}
	if _, err := g.SaveNote("ETH 现货长期拿着，不做合约", "owner", "position", []string{"ETH"}, 0); err != nil {
		t.Fatal(err)
	}

	m := g.MemoryFor("SOL 现在能开多吗")
	if len(m["profile"]) != 2 {
		t.Fatalf("profile %v", m["profile"])
	}
	if len(m["recalled"]) == 0 || m["recalled"][0].Text != "在等 SOL 回踩 140 再开多" {
		t.Fatalf("recalled %v", m["recalled"])
	}
	for _, n := range m["recalled"] {
		if n.Text == "ETH 现货长期拿着，不做合约" {
			t.Fatalf("unrelated memory recalled: %v", m["recalled"])
		}
	}

	// a near-duplicate of the same kind updates the old one instead of adding
	n, err := g.SaveNote("在等 SOL 回踩到 140 再开多", "ai", "plan", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != 2 || len(g.Notes()) != 4 || len(n.Tags) != 1 {
		t.Fatalf("dedupe: %v / %d notes", n, len(g.Notes()))
	}

	if got := g.SearchNotes("ETH", "", 5); len(got) != 1 || got[0].Kind != "position" {
		t.Fatalf("search %v", got)
	}

	// the profile is capped
	distinct := []string{"只做 BTC 和 ETH 的趋势单", "止损放在结构位下方", "周末不开新仓", "单笔风险不超过 2%", "不追涨杀跌", "资金费为负时不做空",
		"偏好 4 小时级别", "盈利一半先止盈", "不在数据发布前开仓", "用逐仓不用全仓", "OI 暴涨时谨慎", "亚洲时段少交易"}
	for _, t := range distinct {
		if _, err := g.SaveNote(t, "owner", "profile", nil, 0); err != nil {
			break // the cap stops it at maxProfile
		}
	}
	if _, err := g.SaveNote("又一条完全不同的画像", "owner", "profile", nil, 0); err == nil {
		t.Fatal("profile cap not enforced")
	}

	// the agent's memory_forget event deletes
	g.onAgentEvent(chatEvent(`{"type":"memory_forget","id":2}`))
	for _, x := range g.Notes() {
		if x.ID == 2 {
			t.Fatal("memory_forget did not delete")
		}
	}
}
