// Adapted from ccusage (MIT) pricing logic for browser-safe cost estimation.

export type LiteLLMModelPricing = {
  input_cost_per_token?: number
  output_cost_per_token?: number
  cache_creation_input_token_cost?: number
  cache_read_input_token_cost?: number
  input_cost_per_token_above_200k_tokens?: number
  output_cost_per_token_above_200k_tokens?: number
  cache_creation_input_token_cost_above_200k_tokens?: number
  cache_read_input_token_cost_above_200k_tokens?: number
}

export type UsageTotals = {
  inputTokens: number
  outputTokens: number
  cacheCreationTokens: number
  cacheReadTokens: number
}

const LITELLM_PRICING_URL =
  "https://raw.githubusercontent.com/BerriAI/litellm/main/model_prices_and_context_window.json"

const DEFAULT_TIERED_THRESHOLD = 200_000

const PROVIDER_PREFIXES = [
  "anthropic/",
  "anthropic.claude-",
  "claude-3-5-",
  "claude-3-",
  "claude-",
  "openrouter/openai/",
]

let pricingCache: Record<string, LiteLLMModelPricing> | null = null
let pricingPromise: Promise<Record<string, LiteLLMModelPricing>> | null = null

function isClaudeModel(modelName: string): boolean {
  const lower = modelName.toLowerCase()
  return lower.startsWith("claude-") || lower.includes("anthropic/claude-") || lower.includes("anthropic.claude-")
}

async function loadPricingDataset(): Promise<Record<string, LiteLLMModelPricing>> {
  if (pricingCache) return pricingCache
  if (pricingPromise) return pricingPromise

  pricingPromise = fetch(LITELLM_PRICING_URL)
    .then(async (response) => {
      if (!response.ok) {
        throw new Error(`Failed to fetch pricing data: ${response.status} ${response.statusText}`)
      }
      const raw = (await response.json()) as Record<string, unknown>
      const dataset: Record<string, LiteLLMModelPricing> = {}
      for (const [modelName, modelData] of Object.entries(raw)) {
        if (modelData == null || typeof modelData !== "object") continue
        if (!isClaudeModel(modelName)) continue
        dataset[modelName] = modelData as LiteLLMModelPricing
      }
      pricingCache = dataset
      return dataset
    })
    .catch(() => {
      pricingCache = {}
      return pricingCache
    })

  return pricingPromise
}

function matchModelPricing(
  dataset: Record<string, LiteLLMModelPricing>,
  modelName: string
): LiteLLMModelPricing | null {
  const candidates = new Set<string>([modelName])
  for (const prefix of PROVIDER_PREFIXES) {
    candidates.add(`${prefix}${modelName}`)
  }

  for (const candidate of candidates) {
    const direct = dataset[candidate]
    if (direct != null) return direct
  }

  const lower = modelName.toLowerCase()
  for (const [key, value] of Object.entries(dataset)) {
    const comparison = key.toLowerCase()
    if (comparison.includes(lower) || lower.includes(comparison)) {
      return value
    }
  }

  return null
}

function calculateTieredCost(
  totalTokens: number | undefined,
  basePrice: number | undefined,
  tieredPrice: number | undefined,
  threshold: number = DEFAULT_TIERED_THRESHOLD
): number {
  if (totalTokens == null || totalTokens <= 0) return 0

  if (totalTokens > threshold && tieredPrice != null) {
    const tokensBelowThreshold = Math.min(totalTokens, threshold)
    const tokensAboveThreshold = Math.max(0, totalTokens - threshold)

    let tieredCost = tokensAboveThreshold * tieredPrice
    if (basePrice != null) {
      tieredCost += tokensBelowThreshold * basePrice
    }
    return tieredCost
  }

  if (basePrice != null) {
    return totalTokens * basePrice
  }

  return 0
}

export function calculateCostFromPricing(tokens: {
  input_tokens: number
  output_tokens: number
  cache_creation_input_tokens?: number
  cache_read_input_tokens?: number
}, pricing: LiteLLMModelPricing): number {
  const inputCost = calculateTieredCost(
    tokens.input_tokens,
    pricing.input_cost_per_token,
    pricing.input_cost_per_token_above_200k_tokens
  )

  const outputCost = calculateTieredCost(
    tokens.output_tokens,
    pricing.output_cost_per_token,
    pricing.output_cost_per_token_above_200k_tokens
  )

  const cacheCreationCost = calculateTieredCost(
    tokens.cache_creation_input_tokens,
    pricing.cache_creation_input_token_cost,
    pricing.cache_creation_input_token_cost_above_200k_tokens
  )

  const cacheReadCost = calculateTieredCost(
    tokens.cache_read_input_tokens,
    pricing.cache_read_input_token_cost,
    pricing.cache_read_input_token_cost_above_200k_tokens
  )

  return inputCost + outputCost + cacheCreationCost + cacheReadCost
}

export async function calculateCostForUsageTotals(
  totalsByModel: Map<string, UsageTotals>
): Promise<number> {
  const dataset = await loadPricingDataset()
  let totalCost = 0

  for (const [modelName, totals] of totalsByModel.entries()) {
    const pricing = matchModelPricing(dataset, modelName)
    if (!pricing) continue

    totalCost += calculateCostFromPricing(
      {
        input_tokens: totals.inputTokens,
        output_tokens: totals.outputTokens,
        cache_creation_input_tokens: totals.cacheCreationTokens,
        cache_read_input_tokens: totals.cacheReadTokens,
      },
      pricing
    )
  }

  return totalCost
}
