package main

import (
	"encoding/csv"
	"fmt"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/ebfe/scard"
)

// ブラウザへ通知を送るためのチャンネル
var cardEventChan = make(chan string, 10)

func main() {
	// 1. NFCリーダーの監視をバックグラウンド（別のゴルーチン）で開始
	go startNFCReader()

	// 2. Webサーバーの設定
	// staticフォルダの中身（index.htmlやmp3）を配信
	http.Handle("/", http.FileServer(http.Dir("./static")))
	// ブラウザとリアルタイム通信するためのエンドポイント
	http.HandleFunc("/events", sseHandler)

	fmt.Println("=========================================")
	fmt.Println(" Webサーバー起動: http://localhost:8080")
	fmt.Println(" ブラウザで上記のURLを開いてください")
	fmt.Println("=========================================")
	
	// ポート8080でサーバーを起動
	log.Fatal(http.ListenAndServe(":8080", nil))
}

// リアルタイム通信（SSE）のハンドラ
func sseHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")

	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "Streaming unsupported!", http.StatusInternalServerError)
		return
	}

	for {
		select {
		// NFCリーダーから学籍番号が送られてきたらブラウザへ送信
		case studentID := <-cardEventChan:
			fmt.Fprintf(w, "data: %s\n\n", studentID)
			flusher.Flush()
		// ブラウザが閉じられたら終了
		case <-r.Context().Done():
			return
		}
	}
}

// これまでのNFC読み取りループ（無限ループ）
func startNFCReader() {
	ctx, err := scard.EstablishContext()
	if err != nil {
		log.Fatalf("PC/SCコンテキスト確立失敗: %v", err)
	}
	defer ctx.Release()

	readers, err := ctx.ListReaders()
	if err != nil || len(readers) == 0 {
		log.Fatalf("リーダーが見つかりません。")
	}

	readerName := readers[0]
	rs := []scard.ReaderState{{Reader: readerName, CurrentState: scard.StateUnaware}}

	for {
		err := ctx.GetStatusChange(rs, 1*time.Second)
		if err != nil {
			if err != scard.ErrTimeout {
				time.Sleep(1 * time.Second)
			}
			continue
		}

		if rs[0].EventState&scard.StatePresent != 0 && rs[0].CurrentState&scard.StatePresent == 0 {
			processCard(ctx, readerName)
		}
		rs[0].CurrentState = rs[0].EventState
	}
}

func processCard(ctx *scard.Context, readerName string) {
	card, err := ctx.Connect(readerName, scard.ShareShared, scard.ProtocolAny)
	if err != nil {
		return
	}
	defer card.Disconnect(scard.LeaveCard)

	// 通信確認
	idmCmd := []byte{0xFF, 0xCA, 0x00, 0x00, 0x00}
	rsp, err := card.Transmit(idmCmd)
	if err != nil || len(rsp) < 2 || rsp[len(rsp)-2] != 0x90 {
		return
	}

	// 芝浦工大のサービスコード(010B)を選択
	selectCmd := []byte{0xFF, 0xA4, 0x00, 0x01, 0x02, 0x0B, 0x01}
	rsp, err = card.Transmit(selectCmd)
	if err != nil || len(rsp) < 2 || (rsp[len(rsp)-2] != 0x90 && rsp[len(rsp)-2] != 0x91) {
		return
	}

	// データ読み出し
	readCmd := []byte{0xFF, 0xB0, 0x00, 0x00, 0x10}
	rsp, err = card.Transmit(readCmd)
	if err != nil || len(rsp) < 18 {
		return
	}

	// 学籍番号抽出（3〜9バイト目）
	studentID := string(rsp[3:10])

	// CSVに保存
	saveToCSV(studentID)

	// ★WebUI（ブラウザ）へ学籍番号をプッシュ送信！
	select {
	case cardEventChan <- studentID:
	default:
		// チャンネルが詰まっている場合はスキップ
	}
}

func saveToCSV(studentID string) {
	file, err := os.OpenFile("students_db.csv", os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return
	}
	defer file.Close()
	writer := csv.NewWriter(file)
	defer writer.Flush()
	writer.Write([]string{time.Now().Format("2006-01-02 15:04:05"), studentID})
}