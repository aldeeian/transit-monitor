import RouteView from "@/components/RouteView";

export default async function RoutePage({
  params,
}: {
  params: Promise<{ id: string }>;
}) {
  const { id } = await params;
  return <RouteView route={decodeURIComponent(id)} />;
}
