// Proof-of-build smoke test: links libi2pd.a + our capi/capi2 wrapper,
// initializes the embedded router, creates a persistent local destination,
// prints its base32 address, then tears everything down. Does not wait for
// full network readiness (tunnel building can take real minutes on a live
// I2P network) — this only proves the embedding compiles, links, and the
// init/destination lifecycle runs without crashing.
#include "capi.h"
#include "capi2.h"
#include <cstdio>
#include <thread>
#include <chrono>

int main()
{
	const char* argv[] = {
		"smoketest",
		"--datadir=/tmp/i2ptest",
		"--log=stdout",
		"--loglevel=warn",
	};
	C_InitI2P (4, const_cast<char**>(argv), "smoketest");
	C_StartI2P ();
	std::printf ("router started\n");

	auto dest = I2PP_CreateDestination ("/tmp/i2ptest/dest.keys", 1);
	if (!dest)
	{
		std::printf ("FAIL: CreateDestination returned null\n");
		return 1;
	}

	char addr[64];
	int n = I2PP_GetBase32Address (dest, addr, sizeof(addr));
	if (n <= 0)
	{
		std::printf ("FAIL: GetBase32Address returned %d\n", n);
		return 1;
	}
	std::printf ("destination: %s.b32.i2p\n", addr);

	std::this_thread::sleep_for (std::chrono::seconds (2));
	std::printf ("ready=%d\n", I2PP_IsDestinationReady (dest));

	I2PP_DestroyDestination (dest);
	C_StopI2P ();
	C_TerminateI2P ();
	std::printf ("OK\n");
	return 0;
}
