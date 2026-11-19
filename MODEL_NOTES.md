# Model notes — local Ollama candidates for the unhinged cast

Measured live on the RTX A5000 (24 GB), Ollama 0.32.15, 2026-11-19.

## Chosen default: dolphin-mistral:latest (4.1 GB)

- Steady-state throughput ~120-130 tok/s; a 1-3 sentence line lands in ~0.4s
  (first call per session costs ~0.9s while the model loads into VRAM).
- Generation is no longer the bottleneck; Discord rate limits are. Budget/rate
  caps become the primary governor, which is what we want for a moderation
  stress-test rig.
- Config: choir.unhinged.example.yaml ollama.model = dolphin-mistral:latest.

## Flavour findings (10-message cast sample)

- Troll, Derailer, OneWord: on-brief and snarky when the prompt is pointed.
- Argumentative, Interrupter, Pip: personality present but dilutes as the
  transcript grows; all converge on "let's find a balance" agreeableness.
- Incoherent: DOES NOT perform — Mistral-7B writes fluent, competent prose even
  when told to be jagged/half-finished. Needs num_predict ~12 + temperature 1.4+
  (or a genuinely jankier tiny model) to actually trail off.

## Full installed inventory (for reference)

- dolphin-mistral:latest    4.1 GB  <- unhinged default (alignment dial low)
- qwen2.5:7b                4.7 GB  <- neutral-class base
- qwen3-vl:2b-instruct      1.9 GB  <- tiny, vision (image-commenting persona)
- deepseek-coder:latest     776 MB  <- smallest; coder-tuned (pedantic)
- ornith:9b                 5.6 GB  <- untested
- qwen3:30b-a3b             18 GB   <- prior default; heavier, more polished
- gpt-oss:20b               13 GB   <- too big alongside a 18GB model on 24GB
- (others: gemma4, glm-4.7-flash, qwen3.8, deepseek-coder-v2, deepseek-v4.1-flash)

## Tuning note

config.go applyDefaults still falls back to qwen3:30b-a3b when ollama.model is
empty; the example config now sets dolphin explicitly so it wins. For per-voice
flavour, a per-persona model override (Dolphin for chaos, qwen2.5:7b for the
neutral eight) is a small, cheap next step worth doing before the first live run.
