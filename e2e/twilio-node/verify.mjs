// Starts or checks a Twilio Verify verification with the stock twilio SDK and
// prints the result as JSON. Caduceus is reached only through HTTPS_PROXY and
// NODE_EXTRA_CA_CERTS, set by e2e_test.go.
//
//   node verify.mjs start TO
//   node verify.mjs check TO CODE
import twilio from "twilio";

const [command, to, code] = process.argv.slice(2);
const client = twilio("ACe2e00000000000000000000000000000", "e2e-token");
const service = client.verify.v2.services("VAe2e00000000000000000000000000000");

let result;
try {
  if (command === "start") {
    result = await service.verifications.create({ to, channel: "sms" });
  } else if (command === "check") {
    result = await service.verificationChecks.create({ to, code });
  } else {
    throw new Error(`unknown command ${command}: want start or check`);
  }
} catch (err) {
  if (!(err instanceof twilio.RestException)) throw err;
  console.log(JSON.stringify({ restException: { status: err.status, code: err.code } }));
  process.exit(0);
}
console.log(
  JSON.stringify({
    sid: result.sid,
    status: result.status,
    valid: result.valid,
    dateCreated: result.dateCreated.toISOString(),
  }),
);
