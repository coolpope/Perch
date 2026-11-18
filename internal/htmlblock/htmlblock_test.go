package htmlblock

import "testing"

func TestParseReadsAReadmeHeader(t *testing.T) {
	blocks, err := Parse(`<p align="center">
  <a href="LICENSE"><img src="https://img.shields.io/badge/x.svg" alt="License"></a>
  <img src="https://img.shields.io/badge/y.svg" alt="Platforms">
</p>
<h1 align="center">Hop</h1>
<p align="center">
  English · <a href="README.zh-CN.md">简体中文</a>
</p>
<p align="center">
  <picture>
    <source media="(prefers-color-scheme: dark)" srcset="dark.png">
    <img src="light.png" width="860" alt="Nodes">
  </picture>
</p>`)
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 4 {
		t.Fatalf("read %d blocks: %+v", len(blocks), blocks)
	}
	badges := blocks[0].Lines[0]
	if blocks[0].Align != AlignCenter || len(badges) != 2 || badges[0].Link != "LICENSE" || badges[1].Image.Alt != "Platforms" {
		t.Errorf("badges %+v", blocks[0])
	}
	if blocks[1].Heading != 1 || blocks[1].Lines[0][0].Text != "Hop" || !blocks[1].Lines[0][0].Bold {
		t.Errorf("heading %+v", blocks[1])
	}
	language := blocks[2].Lines[0]
	if len(language) != 2 || language[0].Text != "English · " || language[1].Link != "README.zh-CN.md" {
		t.Errorf("language line %+v", language)
	}
	picture := blocks[3].Lines[0][0].Image
	if picture.Light != "light.png" || picture.Dark != "dark.png" || picture.Width != 860 {
		t.Errorf("picture %+v", picture)
	}
}

func TestParseSkipsWhatDoesNotShow(t *testing.T) {
	blocks, err := Parse("<!-- note --><script>alert(1)</script><style>p{}</style>")
	if err != nil {
		t.Fatal(err)
	}
	if len(blocks) != 0 {
		t.Errorf("read %+v", blocks)
	}
}
