package storage

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/aliyun/alibaba-cloud-sdk-go/sdk/requests"
	sts "github.com/aliyun/alibaba-cloud-sdk-go/services/sts"
)

type STSProvider struct {
	region    string
	endpoint  string
	bucket    string
	roleARN   string
	ttlSecond int
	client    *sts.Client
}

type UploadSTS struct {
	Region          string `json:"region"`
	Endpoint        string `json:"endpoint"`
	Bucket          string `json:"bucket"`
	AccessKeyID     string `json:"accessKeyId"`
	AccessKeySecret string `json:"accessKeySecret"`
	SecurityToken   string `json:"securityToken"`
	Expiration      string `json:"expiration"`
}

func NewSTSProvider(region, endpoint, bucket, accessKeyID, accessKeySecret, roleARN string, ttlSecond int) (*STSProvider, error) {
	if region == "" || endpoint == "" || bucket == "" || accessKeyID == "" || accessKeySecret == "" || roleARN == "" {
		return nil, fmt.Errorf("invalid sts config: region/endpoint/bucket/access key/role arn are required")
	}
	client, err := sts.NewClientWithAccessKey(region, accessKeyID, accessKeySecret)
	if err != nil {
		return nil, fmt.Errorf("create sts client: %w", err)
	}
	if ttlSecond <= 0 {
		ttlSecond = 900
	}
	if ttlSecond > 3600 {
		ttlSecond = 3600
	}

	return &STSProvider{
		region:    region,
		endpoint:  endpoint,
		bucket:    bucket,
		roleARN:   roleARN,
		ttlSecond: ttlSecond,
		client:    client,
	}, nil
}

func (p *STSProvider) IssueUploadSTS(_ context.Context, sessionName, objectKey string) (*UploadSTS, error) {
	if p == nil || p.client == nil {
		return nil, fmt.Errorf("sts provider not initialized")
	}
	objectKey = strings.TrimPrefix(strings.TrimSpace(objectKey), "/")
	if objectKey == "" {
		return nil, fmt.Errorf("object key is required")
	}

	policyDoc, err := json.Marshal(map[string]any{
		"Version": "1",
		"Statement": []map[string]any{
			{
				"Effect": "Allow",
				"Action": []string{
					"oss:PutObject",
					"oss:GetObject",
				},
				"Resource": []string{
					fmt.Sprintf("acs:oss:*:*:%s/%s", p.bucket, objectKey),
				},
			},
		},
	})
	if err != nil {
		return nil, fmt.Errorf("build sts policy: %w", err)
	}

	req := sts.CreateAssumeRoleRequest()
	req.Scheme = "https"
	req.RoleArn = p.roleARN
	req.RoleSessionName = normalizeSessionName(sessionName)
	req.Policy = string(policyDoc)
	req.DurationSeconds = requests.NewInteger(p.ttlSecond)

	resp, err := p.client.AssumeRole(req)
	if err != nil {
		return nil, fmt.Errorf("assume role: %w", err)
	}
	exp := time.Now().Add(time.Duration(p.ttlSecond) * time.Second).UTC().Format(time.RFC3339)
	if resp.Credentials.Expiration != "" {
		exp = resp.Credentials.Expiration
	}

	return &UploadSTS{
		Region:          p.region,
		Endpoint:        p.endpoint,
		Bucket:          p.bucket,
		AccessKeyID:     resp.Credentials.AccessKeyId,
		AccessKeySecret: resp.Credentials.AccessKeySecret,
		SecurityToken:   resp.Credentials.SecurityToken,
		Expiration:      exp,
	}, nil
}

func normalizeSessionName(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		s = "liteflow-upload"
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '-' || r == '_' || r == '.' {
			b.WriteRune(r)
			continue
		}
		b.WriteByte('-')
	}
	out := b.String()
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}
