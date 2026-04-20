package config

import "time"

type Config struct {
	Port            int      `env:"PORT" envDefault:"8080"`
	Env             string   `env:"APP_ENV" envDefault:"dev"`
	AllowedOrigins  []string `env:"ALLOWED_ORIGINS" envSeparator:"," envDefault:"http://localhost:3000"`
	FrontendURL     string   `env:"FRONTEND_URL" envDefault:"http://localhost:3000"`
	SuperAdminPhone string   `env:"SUPER_ADMIN_PHONE" envDefault:"13426112101"`
	DebugEnabled    bool     `env:"DEBUG_ENABLED" envDefault:"true"`

	DB      DBConfig
	JWT     JWTConfig
	SMS     SMSConfig
	Search  SearchConfig
	LLM     LLMConfig
	MCP     MCPConfig
	Storage StorageConfig
	Feishu  FeishuConfig
	Tasks   TasksConfig
}

type DBConfig struct {
	Host     string `env:"DB_HOST" envDefault:"localhost"`
	Port     int    `env:"DB_PORT" envDefault:"5432"`
	Name     string `env:"DB_NAME" envDefault:"liteflow"`
	Username string `env:"DB_USERNAME" envDefault:"liteflow"`
	Password string `env:"DB_PASSWORD"`
}

type JWTConfig struct {
	Secret                 string `env:"JWT_SECRET" envDefault:"liteflow-default-jwt-secret-key-for-dev-only-32chars"`
	AccessTokenExpiration  int    `env:"JWT_ACCESS_EXPIRATION" envDefault:"7200"`
	RefreshTokenExpiration int    `env:"REFRESH_TOKEN_EXPIRATION" envDefault:"2592000"`
}

func (c JWTConfig) AccessTokenDuration() time.Duration {
	return time.Duration(c.AccessTokenExpiration) * time.Second
}

func (c JWTConfig) RefreshTokenDuration() time.Duration {
	return time.Duration(c.RefreshTokenExpiration) * time.Second
}

type SMSConfig struct {
	Provider       string `env:"SMS_PROVIDER" envDefault:"mock"`
	AliyunKeyID    string `env:"ALIYUN_SMS_KEY_ID"`
	AliyunSecret   string `env:"ALIYUN_SMS_KEY_SECRET"`
	AliyunEndpoint string `env:"ALIYUN_SMS_ENDPOINT" envDefault:"dysmsapi.aliyuncs.com"`
	TemplateCode   string `env:"ALIYUN_SMS_TEMPLATE_CODE"`
}

type SearchConfig struct {
	Provider         string               `env:"SEARCH_PROVIDER" envDefault:"metaso"`
	FetchMode        string               `env:"SEARCH_FETCH_MODE" envDefault:"metaso-api"`
	MaxContentTokens int                  `env:"SEARCH_MAX_CONTENT_TOKENS" envDefault:"3000"`
	FetchTimeout     int                  `env:"SEARCH_FETCH_TIMEOUT" envDefault:"20"`
	Metaso           SearchProviderConfig `envPrefix:"METASO_"`
	Tavily           SearchProviderConfig `envPrefix:"TAVILY_"`
	Bocha            SearchProviderConfig `envPrefix:"BOCHA_"`
}

type SearchProviderConfig struct {
	APIKey   string `env:"API_KEY"`
	Endpoint string `env:"ENDPOINT"`
	Timeout  int    `env:"TIMEOUT_SECONDS" envDefault:"15"`
	Count    int    `env:"DEFAULT_COUNT" envDefault:"5"`
}

type LLMConfig struct {
	DefaultProvider string         `env:"LLM_DEFAULT_PROVIDER" envDefault:"deepseek"`
	DeepSeek        ProviderConfig `envPrefix:"DEEPSEEK_"`
	Qwen            ProviderConfig `envPrefix:"QWEN_"`
	OpenRouter      ProviderConfig `envPrefix:"OPENROUTER_"`
	Cloudflare      CloudflareConfig
}

type ProviderConfig struct {
	APIKey   string `env:"API_KEY"`
	Endpoint string `env:"ENDPOINT"`
	Model    string `env:"MODEL"`
}

type CloudflareConfig struct {
	Endpoint   string `env:"CF_AI_GATEWAY_ENDPOINT"`
	Model      string `env:"CF_AI_GATEWAY_MODEL" envDefault:"@cf/meta/llama-3.1-8b-instruct"`
	Token      string `env:"CF_AIG_TOKEN"`
	ImageModel string `env:"CF_AI_GATEWAY_IMAGE_MODEL" envDefault:"gemini-3-pro-image-preview"`
}

type MCPConfig struct {
	OAuthCallbackURL     string `env:"MCP_OAUTH_CALLBACK_URL"`
	GitHubClientID       string `env:"MCP_OAUTH_GITHUB_CLIENT_ID"`
	GitHubClientSecret   string `env:"MCP_OAUTH_GITHUB_CLIENT_SECRET"`
	GitHubScope          string `env:"MCP_OAUTH_GITHUB_SCOPE" envDefault:"repo,read:org"`
	SupabaseClientID     string `env:"MCP_OAUTH_SUPABASE_CLIENT_ID"`
	SupabaseClientSecret string `env:"MCP_OAUTH_SUPABASE_CLIENT_SECRET"`
	SupabaseScope        string `env:"MCP_OAUTH_SUPABASE_SCOPE" envDefault:"all"`
}

type StorageConfig struct {
	BasePath       string `env:"STORAGE_BASE_PATH" envDefault:"./storage"`
	AliyunEndpoint string `env:"ALIYUN_OSS_ENDPOINT"`
	AliyunBucket   string `env:"ALIYUN_OSS_BUCKET"`
	AliyunKeyID    string `env:"ALIYUN_OSS_KEY_ID"`
	AliyunSecret   string `env:"ALIYUN_OSS_KEY_SECRET"`
}

type FeishuConfig struct {
	Enabled             bool `env:"FEISHU_ENABLED" envDefault:"false"`
	HealthCheckInterval int  `env:"FEISHU_HEALTH_CHECK_MS" envDefault:"60000"`
}

type TasksConfig struct {
	Enabled        bool `env:"TASKS_ENABLED" envDefault:"true"`
	PoolSize       int  `env:"TASKS_POOL_SIZE" envDefault:"4"`
	MaxPerUser     int  `env:"TASKS_MAX_PER_USER" envDefault:"10"`
	MinInterval    int  `env:"TASKS_MIN_INTERVAL" envDefault:"10"`
	MaxTokens      int  `env:"TASKS_MAX_TOKENS" envDefault:"50000"`
	MaxRunsPerDay  int  `env:"TASKS_MAX_RUNS_PER_DAY" envDefault:"24"`
	MaxDailyTokens int  `env:"TASKS_MAX_DAILY_TOKENS" envDefault:"500000"`
	ExecTimeout    int  `env:"TASKS_EXECUTION_TIMEOUT" envDefault:"120"`
	RetentionDays  int  `env:"TASKS_RETENTION_DAYS" envDefault:"30"`
}
