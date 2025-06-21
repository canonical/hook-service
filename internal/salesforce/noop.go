package salesforce

type NoopClient struct {
}

func (c *NoopClient) Query(q string, r any) error {
	return nil
}

func NewNoopClient() *NoopClient {
	return new(NoopClient)
}
