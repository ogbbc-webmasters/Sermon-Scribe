module Timeline exposing (boundaryTime, canonicalize, changeBoundary, decoder, encodeRegions, timestamp, valid)

import Api exposing (Region, Timeline)
import Json.Decode as Decode
import Json.Encode as Encode


decoder : Decode.Decoder Timeline
decoder =
    Decode.map2 Timeline
        (Decode.field "duration" Decode.float)
        (Decode.field "regions" (Decode.list regionDecoder))


regionDecoder : Decode.Decoder Region
regionDecoder =
    Decode.map4 Region
        (Decode.field "start" Decode.float)
        (Decode.field "end" Decode.float)
        (Decode.field "type" legalType)
        (Decode.field "keep" Decode.bool)


legalType : Decode.Decoder String
legalType =
    Decode.string
        |> Decode.andThen
            (\value ->
                if List.member value [ "speaking", "singing", "silence" ] then
                    Decode.succeed value

                else
                    Decode.fail "Unknown region type"
            )


valid : Float -> List Region -> Bool
valid duration regions =
    duration
        > 0
        && (List.head regions |> Maybe.map (\region -> close region.start 0) |> Maybe.withDefault False)
        && (List.reverse regions |> List.head |> Maybe.map (\region -> close region.end duration) |> Maybe.withDefault False)
        && List.all (validRegion duration) regions
        && contiguous regions


validRegion : Float -> Region -> Bool
validRegion duration region =
    region.start
        >= 0
        && region.end
        <= duration
        && region.end
        > region.start
        && List.member region.regionType [ "speaking", "singing", "silence" ]


contiguous : List Region -> Bool
contiguous regions =
    case regions of
        left :: right :: rest ->
            close left.end right.start && contiguous (right :: rest)

        _ ->
            True


close : Float -> Float -> Bool
close left right =
    abs (left - right) < 0.01


canonicalize : List Region -> List Region
canonicalize regions =
    case regions of
        before :: keptStart :: deleted :: keptEnd :: after :: rest ->
            if
                before.regionType
                    /= "silence"
                    && keptStart.regionType
                    == "silence"
                    && keptStart.keep
                    && deleted.regionType
                    == "silence"
                    && not deleted.keep
                    && keptEnd.regionType
                    == "silence"
                    && keptEnd.keep
                    && after.regionType
                    /= "silence"
            then
                { before | end = keptStart.end }
                    :: { deleted | start = keptStart.end, end = keptEnd.start }
                    :: canonicalize ({ after | start = keptEnd.start } :: rest)

            else
                mergeAdjacent regions

        _ ->
            mergeAdjacent regions


mergeAdjacent : List Region -> List Region
mergeAdjacent regions =
    case regions of
        left :: right :: rest ->
            if left.regionType == right.regionType && (left.keep == right.keep || left.regionType == "silence") then
                canonicalize ({ left | end = right.end, keep = left.keep && right.keep } :: rest)

            else
                left :: canonicalize (right :: rest)

        _ ->
            regions


changeBoundary : Int -> Float -> List Region -> List Region
changeBoundary boundary time regions =
    case ( get (boundary - 1) regions, get boundary regions ) of
        ( Just left, Just right ) ->
            let
                next =
                    clamp (left.start + 0.01) (right.end - 0.01) time
            in
            List.indexedMap
                (\index region ->
                    if index == boundary - 1 then
                        { region | end = next }

                    else if index == boundary then
                        { region | start = next }

                    else
                        region
                )
                regions

        _ ->
            regions


boundaryTime : Int -> List Region -> Float
boundaryTime index regions =
    get index regions |> Maybe.map .start |> Maybe.withDefault 0


get : Int -> List a -> Maybe a
get index list =
    List.drop index list |> List.head


clamp : Float -> Float -> Float -> Float
clamp low high value =
    max low (min high value)


encodeRegions : List Region -> Encode.Value
encodeRegions regions =
    Encode.list
        (\region ->
            Encode.object
                [ ( "start", Encode.float region.start )
                , ( "end", Encode.float region.end )
                , ( "type", Encode.string region.regionType )
                , ( "keep", Encode.bool region.keep )
                ]
        )
        regions


timestamp : Float -> String
timestamp seconds =
    let
        whole =
            round (seconds * 100)

        minutes =
            whole // 6000

        remainder =
            toFloat (modBy 6000 whole) / 100

        formattedRemainder =
            String.fromFloat remainder

        decimals =
            formattedRemainder
                |> String.split "."
                |> List.drop 1
                |> List.head
                |> Maybe.withDefault ""
    in
    String.fromInt minutes
        ++ ":"
        ++ (if remainder < 10 then
                "0"

            else
                ""
           )
        ++ formattedRemainder
        ++ (case String.length decimals of
                0 ->
                    ".00"

                1 ->
                    "0"

                _ ->
                    ""
           )
