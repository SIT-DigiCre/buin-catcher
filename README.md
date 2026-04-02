# ぶいんきゃっちゃー

学生証に記録された学籍番号を読み取り、csvに記録するGoアプリケーションです。
main.goを実行時にログ出力されるURLから、WebUIへアクセスできます。

# How To Run

Windows 11で動作確認をしています。

1. SONY公式のNFCポートソフトウェアをインストールしてください。

https://www.sony.co.jp/Products/felica/consumer/support/download/nfcportsoftware.html

2. Goをインストールしてください。

https://go.dev/

3. コンピューターのUSBポートにPaSoRi(RC-S380)を接続します。

4. Goで実行します。

```
go run main.go
```

5. 「学生証をタッチして～」の文言はスペースキーで表示・非表示を切り替えられます。