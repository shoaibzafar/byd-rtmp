package config

import (
	"fmt"

	"github.com/spf13/viper"
)

type Config struct {
	Server   ServerConfig   `mapstructure:"server"`
	RTMP     RTMPConfig     `mapstructure:"rtmp"`
	Logging  LoggingConfig  `mapstructure:"logging"`
	Security SecurityConfig `mapstructure:"security"`
}

type ServerConfig struct {
	Host string `mapstructure:"host"`
	Port int    `mapstructure:"port"`
}

type RTMPConfig struct {
	Port        int    `mapstructure:"port"`
	BufferSize  int    `mapstructure:"buffer_size"`
	MaxStreams  int    `mapstructure:"max_streams"`
	StreamPath  string `mapstructure:"stream_path"`
	RecordPath  string `mapstructure:"record_path"`
	EnableHLS   bool   `mapstructure:"enable_hls"`
	HLSPath     string `mapstructure:"hls_path"`
	SegmentTime int    `mapstructure:"segment_time"`
}

type LoggingConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	File   string `mapstructure:"file"`
}

type SecurityConfig struct {
	EnableAuth bool          `mapstructure:"enable_auth"`
	APIKey     string        `mapstructure:"api_key"`
	AllowedIPs []string      `mapstructure:"allowed_ips"`
	WebAuth    WebAuthConfig `mapstructure:"web_auth"`
}

type WebAuthConfig struct {
	Username       string `mapstructure:"username"`
	Password       string `mapstructure:"password"`
	SessionTimeout int    `mapstructure:"session_timeout"`
}

var defaultConfig = Config{
	Server: ServerConfig{
		Host: "0.0.0.0",
		Port: 8080,
	},
	RTMP: RTMPConfig{
		Port:        1935,
		BufferSize:  4096,
		MaxStreams:  100,
		StreamPath:  "/live",
		RecordPath:  "./recordings",
		EnableHLS:   true,
		HLSPath:     "./hls",
		SegmentTime: 10,
	},
	Logging: LoggingConfig{
		Level:  "info",
		Format: "json",
		File:   "",
	},
	Security: SecurityConfig{
		EnableAuth: false,
		APIKey:     "",
		AllowedIPs: []string{},
		WebAuth: WebAuthConfig{
			Username:       "admin",
			Password:       "paitec123",
			SessionTimeout: 3600,
		},
	},
}

func Load() (*Config, error) {
	viper.SetConfigName("config")
	viper.SetConfigType("yaml")
	viper.AddConfigPath(".")
	viper.AddConfigPath("./config")
	viper.AddConfigPath("/etc/paitec-rtmp")

	// Set defaults
	viper.SetDefault("server.host", defaultConfig.Server.Host)
	viper.SetDefault("server.port", defaultConfig.Server.Port)
	viper.SetDefault("rtmp.port", defaultConfig.RTMP.Port)
	viper.SetDefault("rtmp.buffer_size", defaultConfig.RTMP.BufferSize)
	viper.SetDefault("rtmp.max_streams", defaultConfig.RTMP.MaxStreams)
	viper.SetDefault("rtmp.stream_path", defaultConfig.RTMP.StreamPath)
	viper.SetDefault("rtmp.record_path", defaultConfig.RTMP.RecordPath)
	viper.SetDefault("rtmp.enable_hls", defaultConfig.RTMP.EnableHLS)
	viper.SetDefault("rtmp.hls_path", defaultConfig.RTMP.HLSPath)
	viper.SetDefault("rtmp.segment_time", defaultConfig.RTMP.SegmentTime)
	viper.SetDefault("logging.level", defaultConfig.Logging.Level)
	viper.SetDefault("logging.format", defaultConfig.Logging.Format)
	viper.SetDefault("security.enable_auth", defaultConfig.Security.EnableAuth)
	viper.SetDefault("security.web_auth.username", defaultConfig.Security.WebAuth.Username)
	viper.SetDefault("security.web_auth.password", defaultConfig.Security.WebAuth.Password)
	viper.SetDefault("security.web_auth.session_timeout", defaultConfig.Security.WebAuth.SessionTimeout)

	// Environment variables
	viper.AutomaticEnv()
	viper.SetEnvPrefix("PAITEC_RTMP")

	// Map environment variables
	viper.BindEnv("server.host", "PAITEC_RTMP_SERVER_HOST")
	viper.BindEnv("server.port", "PAITEC_RTMP_SERVER_PORT")
	viper.BindEnv("rtmp.port", "PAITEC_RTMP_RTMP_PORT")
	viper.BindEnv("rtmp.buffer_size", "PAITEC_RTMP_BUFFER_SIZE")
	viper.BindEnv("rtmp.max_streams", "PAITEC_RTMP_MAX_STREAMS")
	viper.BindEnv("logging.level", "PAITEC_RTMP_LOG_LEVEL")
	viper.BindEnv("security.api_key", "PAITEC_RTMP_API_KEY")
	viper.BindEnv("security.web_auth.username", "PAITEC_RTMP_WEBADMIN_USERNAME")
	viper.BindEnv("security.web_auth.password", "PAITEC_RTMP_WEBADMIN_PASSWORD")
	viper.BindEnv("security.web_auth.session_timeout", "PAITEC_RTMP_WEBADMIN_SESSION_TIMEOUT")

	if err := viper.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("error reading config file: %w", err)
		}
	}

	var config Config
	if err := viper.Unmarshal(&config); err != nil {
		return nil, fmt.Errorf("error unmarshaling config: %w", err)
	}

	// Validate configuration
	if err := validateConfig(&config); err != nil {
		return nil, fmt.Errorf("invalid configuration: %w", err)
	}

	return &config, nil
}

func validateConfig(config *Config) error {
	if config.Server.Port <= 0 || config.Server.Port > 65535 {
		return fmt.Errorf("invalid server port: %d", config.Server.Port)
	}

	if config.RTMP.Port <= 0 || config.RTMP.Port > 65535 {
		return fmt.Errorf("invalid RTMP port: %d", config.RTMP.Port)
	}

	if config.RTMP.BufferSize <= 0 {
		return fmt.Errorf("invalid buffer size: %d", config.RTMP.BufferSize)
	}

	if config.RTMP.MaxStreams <= 0 {
		return fmt.Errorf("invalid max streams: %d", config.RTMP.MaxStreams)
	}

	if config.RTMP.SegmentTime <= 0 {
		return fmt.Errorf("invalid segment time: %d", config.RTMP.SegmentTime)
	}

	return nil
}

func (c *Config) GetServerAddr() string {
	return fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)
}

func (c *Config) GetRTMPAddr() string {
	return fmt.Sprintf(":%d", c.RTMP.Port)
}

func (c *Config) IsIPAllowed(ip string) bool {
	if !c.Security.EnableAuth {
		return true
	}

	if len(c.Security.AllowedIPs) == 0 {
		return true
	}

	for _, allowedIP := range c.Security.AllowedIPs {
		if allowedIP == ip {
			return true
		}
	}

	return false
}
