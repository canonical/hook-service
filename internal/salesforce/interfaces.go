package salesforce

type SalesforceInterface interface {
	Query(string, any) error
}
