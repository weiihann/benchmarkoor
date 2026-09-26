# Sourced by the compute drivers. Compute sessions run unpinned with the host's
# own frequency policy, so the host state is recorded rather than set.

# record_host_facts DIR writes CPU, topology, kernel, memory, Docker, governor,
# and boost state into DIR.
record_host_facts() {
  local dir=$1
  mkdir -p "$dir"
  lscpu >"$dir/lscpu.txt"
  lscpu -e=CPU,CORE,SOCKET,NODE,ONLINE,MAXMHZ >"$dir/topology.txt"
  uname -a >"$dir/kernel.txt"
  free -h >"$dir/memory.txt"
  docker info >"$dir/docker-info.txt" 2>&1
  { cat /sys/devices/system/cpu/cpu0/cpufreq/scaling_governor 2>/dev/null || echo unknown; } >"$dir/governor.txt"
  { cat /sys/devices/system/cpu/cpufreq/boost 2>/dev/null || cat /sys/devices/system/cpu/intel_pstate/no_turbo 2>/dev/null || echo unknown; } >"$dir/boost.txt"
}
