// Sends one SMS with the stock twilio SDK. Caduceus is reached only through
// HTTP_PROXY and NODE_EXTRA_CA_CERTS, set by e2e_test.go.
import twilio from "twilio";

const [to, body] = process.argv.slice(2);
const client = twilio("ACe2e00000000000000000000000000000", "e2e-token");
const msg = await client.messages.create({ to, from: "+15559990000", body });
console.log(msg.sid);
