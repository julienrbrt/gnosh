// Package llm talks to model services. It turns a core.Request into an HTTP
// request in one of three wire formats, reads the answer as a stream of
// server-sent events, and hands it on as core.Events. It uses nothing but
// the standard library, and it was written from the formats' public
// documentation (the list is at the end).
//
// New makes a core.Provider from a Config. The Config's Type picks the wire
// format:
//
//	"openai"     the chat-completions format: POST {base}/chat/completions.
//	             OpenAI, OpenRouter, Groq, vLLM, Ollama's /v1 and llama.cpp
//	             all speak it. Also a core.ModelLister.
//	"llamacpp"   the same format, plus what llama.cpp's server adds to it.
//	             Also a core.ModelLister and a core.TokenCounter.
//	"anthropic"  Anthropic's Messages API: POST {base}/v1/messages.
//	             Also a core.ModelLister and a core.TokenCounter.
//
// Config.BaseURL is required: no request goes anywhere the configuration
// did not name. An address may be given with its "/v1" or without where
// that is safe: always for anthropic and llamacpp, and for openai when the
// address is nothing but a host.
//
// # What every provider does
//
// A provider does not retry. Every failure is reported once, sorted into a
// kind, and the caller decides what to do about it.
//
// A stream is read on the goroutine that ranges over it. The request is
// sent when the range begins and the connection is closed before the range
// statement returns, whether the stream ended well, failed, ran into a
// time limit, was cancelled, or the consumer left the loop early. The
// provider's own HTTP client opens one HTTP/1.1 connection per request and
// closes it afterwards, so nothing, neither a goroutine nor a connection,
// outlives a stream. Ranging over the same sequence again sends the
// request again.
//
// A stream that ends well ends with exactly one EventStop, its last event.
// A stream that fails ends with an error instead and has no EventStop;
// every piece of the answer that arrived before the failure has been
// handed on by then. An answer with nothing in it, no text and no tool
// call, is a stream that ends well: the caller decides what an empty
// answer means.
//
// Cancelling the context ends the stream with the context's own error, so
// errors.Is(err, context.Canceled) holds. Every other failure is a
// *core.ProviderError.
//
// Request bodies are JSON objects. Config.Extra and then Request.Extra are
// merged into the body the provider built: objects are merged key by key,
// any other value replaces what was there, and a nil value removes a
// field. That is the way to send a field the package does not know, and to
// take out one a server does not like.
//
// Request headers are few: Content-Type, Accept, "User-Agent: gnosh", and
// the key, if there is one ("Authorization: Bearer" for openai and
// llamacpp, "x-api-key" and "anthropic-version: 2023-06-01" for
// anthropic). Config.Headers are set after these and win; an empty value
// removes a header. Nothing identifies the machine or its user. Proxies
// are taken from the environment, as net/http does.
//
// # Time limits
//
// Timeouts holds four limits; zero means none. Each one that runs out ends
// the request with a *core.ProviderError of kind core.ErrTimeout whose
// Timeout field names it.
//
//	"connect"      until there is a connection to the server
//	"first_token"  from sending the request until the first event that
//	               carries some of the answer
//	"idle"         the longest silence after that
//	"total"        the whole request
//
// The connect limit ends when the connection is ready (the name is looked
// up, the server or proxy is dialled, TLS is done), not when the response
// headers arrive. A server need not send its headers before it has
// something to say: one written with Go's net/http sends them with its
// first write, which for a model server is the first token, and a server
// that waits can still answer a request that fails with an error status.
// Counting the wait for headers against "connect" would make a short
// connect limit, which is what catches a dead tunnel quickly, cut off
// every long prompt on a local model. That wait belongs to "first_token",
// which exists because a local model can spend minutes reading a prompt.
//
// What counts as the answer beginning: a piece of text or of thinking, a
// tool call, a thinking signature, and in the Messages format the opening
// of any content block, because thinking that the API is asked not to show
// opens a block and then says nothing until it is done. Usage reports,
// role-only chunks, pings and comments do not count.
//
// The idle limit runs only while the provider is waiting for the server.
// The time the consumer of the stream takes over an event is not held
// against the server. Any bytes the server sends end the wait, a
// keep-alive comment included.
//
// Once a stream has said why the model stopped, the answer is whole. If
// the connection then breaks or goes silent before the stream's last
// lines, the stream still ends well; only the trailing usage report may be
// missing.
//
// # Errors
//
// A failure's kind comes from one table, errorRules in errors.go, which
// the three formats share. What a failure says about itself, its error
// code and its words, is looked at before its bare HTTP status:
//
//	context_too_long  context_length_exceeded (OpenAI), "prompt is too
//	                  long" and "exceed context limit" (Anthropic),
//	                  exceed_context_size_error and "exceeds the available
//	                  context size" (llama.cpp), "maximum context length",
//	                  "request too large", HTTP 413
//	auth              401, 402, 403; and running out of money or quota
//	                  under any status, insufficient_quota with its 429
//	                  included, because waiting does not bring it back
//	rate_limit        429, rate_limit_error, rate_limit_exceeded; and a
//	                  402 that names a wait
//	overloaded        503, 529, overloaded_error, any message that says
//	                  "overloaded"
//	server            other 5xx, 408, 409, api_error, server_error; and an
//	                  error in the middle of a stream that says nothing
//	                  recognisable
//	invalid_request   other 4xx, a redirect, a request that cannot be
//	                  encoded
//	network           the server cannot be reached, or the connection
//	                  broke
//	bad_response      the stream cannot be read, or ended before the model
//	                  said why it stopped, or was not a stream
//	timeout           one of the four limits above
//
// RetryAfter comes from the retry-after-ms header, then Retry-After (as
// seconds or as a date), then the body: Google's retryDelay, and the "try
// again in 1.5s" and "retry after 26 seconds" that OpenAI, Groq and Azure
// put in their messages. Body holds the first 2048 bytes of what the
// server sent. An error inside a stream has no HTTP status of its own;
// Status is then the one the error names, if it names one, and zero
// otherwise.
//
// A server that cannot take images refuses the whole request: llama.cpp's
// server started without its vision part says "image input is not
// supported" (HTTP 500), and others serving a model for text alone say
// it in their own words (images.go, refusals). A provider told that sends
// the request again at once with each image as a line that says it was
// left out, and from then on never sends one, in a stream or a count of
// tokens. A server that takes images but could not read one says so for
// the whole request too: llama.cpp's says "Failed to load image or audio
// file" (HTTP 400) of a WebP, which its loader does not read, or of a
// broken file. Then the images of the request it has not taken before are
// each sent as a line saying it could not read them, from then on, and the
// request goes again (all of them, when it had taken every one before).
// Without that, a picture a tool read would fail every later request of
// the session the same way, since the conversation keeps it. The caller's
// request is not changed.
//
// When does a stream that ends without a stop reason still count? Only if
// the server sent its own last word ("[DONE]", or "message_stop") and some
// answer arrived. The server plainly meant to stop there, so the reason is
// read off the answer: tool_use if it holds a tool call, end_turn if not.
// A connection that just closes without a reason is a cut-off stream, and
// that is bad_response, whatever arrived.
//
// # Server-sent events
//
// The reader in sse.go follows the format's standard: lines end with LF,
// CR or CRLF; a line that starts with a colon is a comment; "data:" lines
// pile up into one event; a blank line ends it; a byte order mark at the
// start is skipped. Lines can be of any length. It is more lenient than
// the standard in two places: an event with a name and no data is still an
// event, and an event the stream ends in the middle of, without the blank
// line or without any line ending, is handed on if the stream ended
// cleanly. One event may hold up to 64 MiB.
//
// # The chat-completions format ("openai")
//
// The request:
//
//	Request.System     one "system" message, the parts that are not empty
//	                   joined by blank lines
//	user message       one "user" message. Text alone is a string, its
//	                   blocks joined by blank lines; with images it is a
//	                   list of parts, each image an image_url part holding
//	                   a data URL
//	tool_result block  a "tool" message of its own, placed before the rest
//	                   of the user message it came in. Its content is the
//	                   result's text. Images in a result go into the user
//	                   message that follows, under a line that says which
//	                   call they belong to, and the tool message says they
//	                   are there. IsError has no field in this format: a
//	                   failed result says so in its own words, and one
//	                   with no words is given a line that says it failed
//	assistant message  one "assistant" message: its text blocks, joined
//	                   with nothing between them, in content; its tool_use
//	                   blocks in tool_calls, their arguments as a JSON
//	                   string
//	thinking block     left out, unless Config.HistoryThinking is "keep";
//	                   then the message's thinking goes in
//	                   reasoning_content
//	Request.Tools      tools[].function: name, description, parameters
//	MaxTokens          max_completion_tokens (see Config.MaxTokensField)
//	Temperature, TopP  temperature, top_p
//	Stop               stop
//	Thinking.Effort    reasoning_effort; Mode and BudgetTokens are ignored
//	always             stream: true, stream_options: {include_usage: true}
//
// The stream:
//
//	delta.content                text delta
//	delta.reasoning_content, or  thinking delta; a chunk with both is read
//	delta.reasoning              once
//	delta.refusal                text delta
//	delta.tool_calls[]           tool_use start, deltas and stop
//	finish_reason                the stop reason, below
//	usage                        usage: prompt_tokens_details.cached_tokens
//	                             goes in CacheReadTokens, cache_write_tokens
//	                             in CacheWriteTokens, and both are taken out
//	                             of prompt_tokens to give InputTokens
//	"[DONE]"                     the end
//	{"error": ...}               an error in the middle of the stream
//
// Tool calls are keyed by their index: the first piece brings id and name,
// the rest bring the arguments in fragments. Servers differ, and the
// decoder takes what comes: a call sent whole; arguments sent as an object
// rather than a string; an index used again for the next call; no index at
// all; a name that arrives after the first arguments; the older single
// function_call. A call with an empty id, or an id the answer already
// used, gets one made up (Config.NewID). The tool_use stop events are sent
// when the answer ends, in the order the calls began.
//
// Stop reasons:
//
//	stop            end_turn; a stop string cannot be told from a natural
//	                stop in this format
//	tool_calls      tool_use (also function_call, and llama.cpp's "tool")
//	length          max_tokens
//	content_filter  refusal
//	anything else   end_turn
//
// Whether the reason is tool_use is read off the answer in the end, since
// servers get it wrong both ways: an answer with tool calls is tool_use
// even under "stop", and an answer without any is end_turn even under
// "tool_calls". The cap and the filter win over both.
//
// Models asks GET {base}/models, and fills in the context window and
// output cap where the service reports them (OpenRouter, Groq, vLLM).
// The format has no way to count tokens, so an openai provider is not a
// core.TokenCounter.
//
// # llama.cpp's additions ("llamacpp")
//
// The request also carries:
//
//	always              cache_prompt: true
//	Thinking.Mode       chat_template_kwargs: {enable_thinking: true|false}
//	                    for "on" and "off"; nothing when left to the model
//	Thinking.Effort     chat_template_kwargs: {reasoning_effort}, in place
//	                    of the top-level field. Both go to the model's chat
//	                    template: a model that thinks or does not (Qwen)
//	                    reads the first, one with levels (gpt-oss) the
//	                    second
//	Thinking.BudgetTokens reasoning_budget_tokens, with BudgetNote as
//	                    reasoning_budget_message, unless Mode is "off": the
//	                    server ends the thinking there and puts the note at
//	                    its end (server builds from b9982 on; an older one
//	                    ignores both)
//	Request.Tools       parallel_tool_calls: true when there are tools; the
//	                    server allows one call per answer otherwise
//	MaxTokens           max_tokens
//
// The stream also carries timings in its last chunks. They give the usage
// its split: prompt_n is InputTokens, cache_n is CacheReadTokens, and
// predicted_n is OutputTokens when there is no usage chunk. From a server
// too old to report cache_n, CacheReadTokens is what is left of the usage
// chunk's prompt_tokens after prompt_n. The speeds go to Config.Trace as a
// TraceTimings record.
//
// Text between <think> and </think> at the start of the answer becomes
// thinking deltas (Config.ThinkTags; on by default for this type). The
// tags may be cut anywhere by the server's chunking. A tag that shows up
// after the answer's own words have begun is left as text, so that a
// model writing about think tags is not cut short. If the server sends
// the reasoning in reasoning_content as well, the copy inside the tags is
// dropped.
//
// Models asks /v1/models and then, of a server that runs one model, its
// /props for n_ctx, the context size the model is running with. A server
// that holds several models loads a model when a request names it, and
// puts out the one in use; so Models asks such a server for its list and
// nothing more, never names a model, and leaves the ContextWindow of its
// models at zero, which means unknown.
//
// CountTokens asks POST /v1/chat/completions/input_tokens, which takes a
// chat-completions body. A server too old to have it answers 404, and the
// count is then made by rendering the prompt with POST /apply-template
// and tokenizing it with POST /tokenize; that count leaves images out.
//
// # The Messages format ("anthropic")
//
// The request. Blocks map one to one:
//
//	Request.System     system: one text block for each part that is not
//	                   empty
//	text block         {type: text}; left out if empty or all white space,
//	                   which the API refuses
//	image block        {type: image, source: {type: base64, ...}}
//	tool_use block     {type: tool_use, id, name, input}
//	tool_result block  {type: tool_result, tool_use_id, content, is_error}
//	thinking block     with a signature: {type: thinking, thinking,
//	                   signature}, sent back as it came, an empty text
//	                   included. With an opaque payload and no signature:
//	                   {type: redacted_thinking, data}. With neither, it
//	                   came from another provider and is left out. All are
//	                   left out if Config.HistoryThinking is "drop"
//	a message with     left out; the API joins the messages on either
//	nothing left       side of it
//	Request.Tools      tools: name, description, input_schema
//	MaxTokens          max_tokens. The API requires it, so a Request
//	                   without one gets Config.DefaultMaxTokens (64000),
//	                   raised by the thinking budget if that would not fit
//	Temperature, TopP  temperature, top_p
//	Stop               stop_sequences
//	Thinking           Mode "off": {type: disabled}. A BudgetTokens:
//	                   {type: enabled, budget_tokens}. Mode "on" without
//	                   a budget: {type: adaptive}. Left to the model:
//	                   nothing. Effort: output_config: {effort}
//	always             stream: true
//
// Which thinking settings a model accepts differs by model generation,
// and the API says so with an invalid_request error; the package sends
// what it is asked to.
//
// Prompt-cache breakpoints (Config.PromptCache; on by default). The API
// caches a prompt as a prefix, in the order tools, system, messages, and
// takes at most four cache_control marks. They go on the last tool, on
// the last block of the last message, and with the two or three that are
// left on the end of each system part, first parts first. Thinking blocks
// cannot carry a mark, so the mark on the last message goes on the last
// block that can. Config.CacheTTL "1h" asks for the hour-long cache.
//
// The stream:
//
//	message_start        usage
//	content_block_start  text: nothing yet. tool_use: tool_use start.
//	                     redacted_thinking: thinking signature, with the
//	                     data as Opaque
//	content_block_delta  text_delta: text delta. thinking_delta: thinking
//	                     delta. input_json_delta: tool_use delta.
//	                     signature_delta: kept for the block's end
//	content_block_stop   thinking: thinking signature. tool_use: tool_use
//	                     stop
//	message_delta        the stop reason; usage (the counts are running
//	                     totals, and each usage event carries all four)
//	message_stop         the end
//	ping                 nothing
//	error                an error in the middle of the stream
//
// Blocks of the API's own tools (server_tool_use and their results) and
// events and blocks of kinds added later are passed over, as the API's
// documentation asks.
//
// Stop reasons:
//
//	end_turn                       end_turn
//	tool_use                       tool_use
//	max_tokens                     max_tokens
//	stop_sequence                  stop_sequence
//	refusal                        refusal
//	model_context_window_exceeded  max_tokens: the answer was cut short
//	pause_turn                     end_turn: a pause in the API's own
//	                               tools, which core has no word for
//
// As with chat completions, an answer that holds a tool call ends with
// tool_use even if the reason says end_turn, and one that holds none ends
// with end_turn even if the reason says tool_use.
//
// Models asks GET /v1/models, following the list's pages, and reports
// max_input_tokens as the context window and max_tokens as the output
// cap. CountTokens asks POST /v1/messages/count_tokens with the model,
// system, tools, messages and thinking settings of the request; of the
// extra fields it sends only those the endpoint accepts.
//
// # Trace
//
// Config.Trace is told each request (method, URL without query or
// password, body), each response (status and headers; the body too when
// it was read whole), each event of a stream, each line of a stream that
// was not part of an event, and llama.cpp's timings. It is never told a
// request header, so it never sees a key.
//
// # Sources
//
// The package was written from these public documents and from no other
// program's source:
//
//   - Server-sent events: https://html.spec.whatwg.org/multipage/server-sent-events.html
//   - OpenAI's API description: https://github.com/openai/openai-openapi
//     and https://developers.openai.com/api/docs/guides/error-codes
//   - llama.cpp's server: https://github.com/ggml-org/llama.cpp/blob/master/tools/server/README.md
//     and https://github.com/ggml-org/llama.cpp/blob/master/docs/function-calling.md
//   - Anthropic's API: https://platform.claude.com/docs/en/api/messages/create,
//     .../build-with-claude/streaming, .../api/errors,
//     .../build-with-claude/prompt-caching, .../build-with-claude/thinking,
//     .../build-with-claude/handling-stop-reasons, .../api/rate-limits,
//     .../build-with-claude/token-counting, .../api/models/list,
//     .../api/versioning,
//     .../build-with-claude/preserved-thinking/prefix-checks
//   - OpenRouter's stream and error formats: https://openrouter.ai/docs/api/reference/streaming
//     and https://openrouter.ai/docs/api/reference/errors-and-debugging
package llm
