package service

import "strings"

// otokIsInvalidEncryptedContextError 识别上游对「加密推理上下文失效」的非标准报错写法，
// 让 Forward 的 HTTP 路径走已有的 invalid_encrypted_content 恢复：剥离 encrypted_content
// 后同号重试一次，并按会话记下失效密文（lineage），后续轮次进场时预先剥离。
//
// 背景：同一 Codex 会话在不同上游供应商之间切换（故障转移或调度换号）时，请求里带的
// reasoning.encrypted_content 是原供应商那边生成的，新上游解不开。OpenAI 原生返回
// code=invalid_encrypted_content，上游已能处理；但部分中转上游换了写法：
//   - code=encrypted_context_invalid："The upstream could not validate encrypted
//     continuation context. Rebuild the conversation context in the client."
//   - code=invalid_request，只给笼统文案："The request could not be processed.
//     Please check the request parameters."
//
// 第二种含义不明确，但恢复逻辑只在请求里确实带加密推理 / compaction 项时才重试
// （trimOpenAIEncryptedReasoningItems 返回 false 就原样透传 400），误判的代价是多一次
// 很快返回的 400，以及该会话之后不再带这批密文。
func otokIsInvalidEncryptedContextError(upstreamCode, upstreamMsg string) bool {
	switch strings.ToLower(strings.TrimSpace(upstreamCode)) {
	case "encrypted_context_invalid":
		return true
	case "invalid_request":
		msg := strings.ToLower(upstreamMsg)
		return strings.Contains(msg, "the request could not be processed") &&
			strings.Contains(msg, "check the request parameters")
	}
	return false
}
