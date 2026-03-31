package main

import (
	"encoding/csv"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"time"

	"github.com/ebfe/scard"
)

// ブラウザへ通知を送るためのチャンネル
var cardEventChan = make(chan string, 10)

func init() {
	// デフォルトではログを出力するが、環境変数 LOG_QUIET=1 のときのみ抑制する
	if os.Getenv("LOG_QUIET") == "1" {
		log.SetOutput(io.Discard)
	}
}

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
		// NFCリーダーからデータが送られてきたらブラウザへ送信
		case dataStr := <-cardEventChan:
			fmt.Fprintf(w, "data: %s\n\n", dataStr)
			flusher.Flush()
		// ブラウザが閉じられたら終了
		case <-r.Context().Done():
			return
		}
	}
}

// NFC読み取りループ
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

// カードの処理（物理カード専用）
func processCard(ctx *scard.Context, readerName string) {
	card, err := ctx.Connect(readerName, scard.ShareShared, scard.ProtocolAny)
	if err != nil {
		return
	}
	defer card.Disconnect(scard.LeaveCard)

	// 通信確認 (IDmの取得)
	idmCmd := []byte{0xFF, 0xCA, 0x00, 0x00, 0x00}
	rsp, err := card.Transmit(idmCmd)
	
	// 物理のFeliCaカードであれば、10バイト以上のレスポンスが返ります
	if err != nil || len(rsp) < 10 {
		return
	}

	// ステータスワード（SW1/SW2）の確認（末尾2バイト）
	statusWord := rsp[len(rsp)-2 : len(rsp)]
	if statusWord[0] != 0x90 && statusWord[0] != 0x91 {
		return
	}

	// 1. まずは学生証かチェック
	if tryReadStudentCard(card) {
		return
	}

	// 2. 学生証でなければ交通系IC（物理）かチェック
	if tryReadICCard(card) {
		return
	}
}

// 学生証読み取り処理
func tryReadStudentCard(card *scard.Card) bool {
	// 芝浦工大のサービスコード(010B)を選択
	selectCmd := []byte{0xFF, 0xA4, 0x00, 0x01, 0x02, 0x0B, 0x01}
	rsp, err := card.Transmit(selectCmd)
	
	if err != nil || len(rsp) < 2 {
		return false
	}

	statusWord := rsp[len(rsp)-2 : len(rsp)]
	if statusWord[0] != 0x90 && statusWord[0] != 0x91 {
		return false
	}

	// データ読み出し
	readCmd := []byte{0xFF, 0xB0, 0x00, 0x00, 0x10}
	rsp, err = card.Transmit(readCmd)
	if err != nil || len(rsp) < 18 {
		return false
	}

	// 学籍番号抽出（3〜9バイト目）
	studentID := string(rsp[3:10])

	// CSVに保存
	saveToCSV(studentID)

	// JSON形式で作成
	cardData := map[string]interface{}{
		"type":      "student",
		"student_id": studentID,
		"card_name": "SIT Student Card",
	}
	jsonData, err := json.Marshal(cardData)
	if err != nil {
		log.Printf("学生証データのJSON変換失敗: %v", err)
		return false
	}

	// ブラウザへ送信
	select {
	case cardEventChan <- string(jsonData):
	default:
	}
	return true
}

// 交通系ICカード（物理）読み取り処理
func tryReadICCard(card *scard.Card) bool {
	// 1. 交通系ICの履歴・残高サービスコード(090F)を選択
	selectCmd := []byte{0xFF, 0xA4, 0x00, 0x01, 0x02, 0x0F, 0x09}
	rsp, err := card.Transmit(selectCmd)
	
	if err != nil || len(rsp) < 2 {
		return false
	}

	statusWord := rsp[len(rsp)-2:]
	if statusWord[0] != 0x90 && statusWord[0] != 0x91 {
		return false
	}

	// 2. データ読み出し (最新の履歴であるブロック0を読み出す)
	readCmd := []byte{0xFF, 0xB0, 0x00, 0x00, 0x10}
	rsp, err = card.Transmit(readCmd)
	if err != nil || len(rsp) < 18 {
		return false
	}

	// 3. 残高データの抽出 (10バイト目と11バイト目)
	balance := int(rsp[10]) | (int(rsp[11]) << 8)
	
	// JSON形式で作成
	cardData := map[string]interface{}{
		"type":      "ic_card",
		"balance":   balance,
		"card_name": "IC Card",
	}
	jsonData, err := json.Marshal(cardData)
	if err != nil {
		log.Printf("交通系ICカードデータのJSON変換失敗: %v", err)
		return false
	}

	// ブラウザへ送信
	select {
	case cardEventChan <- string(jsonData):
	default:
	}
	
	return true
}

// CSV保存処理
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