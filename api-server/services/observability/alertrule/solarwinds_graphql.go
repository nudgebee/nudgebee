package alertrule

import (
	"crypto/sha256"
	"encoding/hex"
)

// SolarWinds has no public API for alert rules: its REST v1 exposes no alert
// endpoints at all, and alert read/write is reachable only as GraphQL over
// POST /v1/tfproxy. That proxy allowlists requests by the EXACT BYTES of the
// GraphQL document, framing included — it does not parse and re-serialise, so
// a semantically identical document with different whitespace is rejected.
//
// Measured against the na-01 proxy on 2026-10-13:
//
//	correct framing          -> 200 with data
//	no surrounding newlines  -> 403 "GraphQL request is not allowed."
//	no operationName         -> hangs, then 504
//
// The User-Agent is irrelevant. The documents below are therefore lifted
// verbatim from the SolarWinds Terraform provider binary, which is the only
// published source of documents the allowlist accepts:
//
//	terraform-provider-swo v0.2.17 (linux_arm64)
//	binary sha256 568619bf54e5fbb06d2beba77f011654ed262aa7ce59653f40333afb9d199a69
//
// DO NOT REFORMAT, RE-INDENT OR RE-WRAP the document bodies. They are tab
// indented because the provider emits them that way, and every byte is part of
// the key. swoDocumentDigests pins them; TestSolarWindsDocumentsMatchTheirDigests
// fails offline the moment one drifts, which is the only protection available
// because the allowlist itself is a server-side property that CI cannot reach.
//
// If SolarWinds changes these documents, this breaks in production with a 403
// and no code change on our side. The durable fix is a supported alert API from
// the vendor, not a newer copy of these bytes.

// swoFrame reproduces the provider's own request framing: one leading and one
// trailing newline around the document body. Load-bearing — see above.
func swoFrame(body string) string { return "\n" + body + "\n" }

// swoOpGetAlertDefinitionByID reads one alert by id. Its selection set is fixed
// by the allowlist and cannot be extended, which is why it is worth recording
// what it returns: actions{configurationIds,resendIntervalSeconds},
// triggerResetActions, noDataResetSeconds, runbookLink, severity, enabled,
// targetEntityTypes, triggerDelaySeconds, conditionType and flatCondition.
// That is every field a later read-merge-write needs in order to preserve an
// alert's notification wiring instead of replacing it with empty values.
const swoOpGetAlertDefinitionByID = "getAlertDefinitionById"

const swoGetAlertDefinitionByIDBody = `query getAlertDefinitionById ($id: ID!) {
	alertQueries {
		alertDefinitions(filter: {id:$id}) {
			alertDefinitions {
				actions {
					configurationIds
					type
					receivingType
					includeDetails
					resendIntervalSeconds
				}
				triggerResetActions
				conditionType
				flatCondition {
					id
					links {
						name
						values
					}
					value {
						dataType
						entityFilter {
							fields {
								fieldName
								rules {
									negate
									type
									value
								}
							}
							ids
							types
							query
						}
						groupByMetricTag
						values
						fieldName
						metricFilter {
							id
							links {
								name
								values
							}
							value {
								operation
								propertyName
								propertyValue
								propertyValues
							}
						}
						operator
						type
						value
						namespace
						query
					}
				}
				description
				enabled
				id
				name
				organizationId
				runbookLink
				severity
				triggered
				triggeredTime
				triggerDelaySeconds
				noDataResetSeconds
				templateId
				targetEntityTypes
				muteInfo {
					muted
					until
				}
				user {
					id
				}
				createdAt
			}
		}
	}
}`

// swoAlertDocuments maps each operation name to the framed document to send.
var swoAlertDocuments = map[string]string{
	swoOpGetAlertDefinitionByID: swoFrame(swoGetAlertDefinitionByIDBody),
}

// swoDocumentDigests pins the bytes the allowlist accepted on 2026-10-13.
var swoDocumentDigests = map[string]string{
	swoOpGetAlertDefinitionByID: "41588f7813a31578c7ce058981deb4139824c4924dcdaff5a5a20e1cc04db2e0",
}

// swoDocumentDigest is the digest of the framed document actually held for an
// operation, so the test and any diagnostic agree on how it is computed.
func swoDocumentDigest(operationName string) string {
	sum := sha256.Sum256([]byte(swoAlertDocuments[operationName]))
	return hex.EncodeToString(sum[:])
}
